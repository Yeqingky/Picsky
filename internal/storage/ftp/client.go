package ftp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

// 连接与传输超时: 单条命令与单次数据读写都使用同一上限.
const (
	dialTimeout = 15 * time.Second
	opTimeout   = 5 * time.Minute
)

// FTP 应答码.
const (
	statusFileOk          = 125 // 数据连接已打开, 开始传输
	statusFileOpen        = 150 // 文件状态正常, 准备打开数据连接
	statusFileUnavailable = 550
)

// client 是单次操作使用的 FTP 控制连接; 不做连接复用.
type client struct {
	conn   net.Conn
	reader *bufio.Reader
	host   string
	stop   func() bool
}

// deadlineNow 返回当前操作的数据读写截止时间.
func deadlineNow() time.Time {
	return time.Now().Add(opTimeout)
}

// dialFTP 连接并登录 FTP 服务器.
func dialFTP(ctx context.Context, addr, username, password string) (*client, error) {
	if strings.ContainsAny(username, "\r\n") || strings.ContainsAny(password, "\r\n") {
		return nil, errors.New("FTP 凭据不能包含换行")
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 FTP 服务器失败: %w", err)
	}
	c := &client{conn: conn, reader: bufio.NewReader(conn), host: connHost(conn)}
	c.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })

	code, msg, err := c.reply()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("读取 FTP 欢迎信息失败: %w", err)
	}
	if code != 220 {
		c.Close()
		return nil, fmt.Errorf("FTP 服务器拒绝连接: %d %s", code, msg)
	}

	user := username
	if user == "" {
		user = "anonymous"
	}
	pass := password
	if user == "anonymous" && pass == "" {
		pass = "anonymous@"
	}
	code, msg, err = c.command(ctx, "USER %s", user)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("FTP 登录失败: %w", err)
	}
	switch code {
	case 230:
	case 331, 332:
		code, msg, err = c.command(ctx, "PASS %s", pass)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("FTP 登录失败: %w", err)
		}
		if code != 230 && code != 202 {
			c.Close()
			return nil, fmt.Errorf("FTP 登录失败: %d %s", code, msg)
		}
	default:
		c.Close()
		return nil, fmt.Errorf("FTP 登录失败: %d %s", code, msg)
	}

	// 可选命令: 失败不影响后续操作.
	_, _, _ = c.command(ctx, "OPTS UTF8 ON")
	_, _, _ = c.command(ctx, "TYPE I")
	return c, nil
}

// connHost 返回控制连接的对端地址, 数据连接默认复用该地址.
func connHost(conn net.Conn) string {
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return addr.IP.String()
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	return host
}

// command 发送一条命令并读取应答.
func (c *client) command(ctx context.Context, format string, args ...any) (int, string, error) {
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	line := fmt.Sprintf(format, args...)
	if strings.ContainsAny(line, "\r\n") {
		return 0, "", errors.New("FTP 命令包含非法换行")
	}
	if err := c.conn.SetDeadline(deadlineNow()); err != nil {
		return 0, "", err
	}
	if _, err := io.WriteString(c.conn, line+"\r\n"); err != nil {
		return 0, "", fmt.Errorf("发送 FTP 命令失败: %w", err)
	}
	return c.reply()
}

// reply 读取一条 (可能多行的) FTP 应答.
func (c *client) reply() (int, string, error) {
	first, err := c.readLine()
	if err != nil {
		return 0, "", err
	}
	if len(first) < 3 {
		return 0, "", fmt.Errorf("FTP 应答格式非法: %q", first)
	}
	code, err := strconv.Atoi(first[:3])
	if err != nil {
		return 0, "", fmt.Errorf("FTP 应答格式非法: %q", first)
	}
	lines := []string{first}
	if len(first) > 3 && first[3] == '-' {
		terminator := first[:3] + " "
		for {
			line, err := c.readLine()
			if err != nil {
				return 0, "", err
			}
			lines = append(lines, line)
			if strings.HasPrefix(line, terminator) {
				break
			}
			if len(lines) > 1000 {
				return 0, "", errors.New("FTP 应答行数超过上限")
			}
		}
	}
	return code, strings.Join(lines, "\n"), nil
}

func (c *client) readLine() (string, error) {
	if err := c.conn.SetDeadline(deadlineNow()); err != nil {
		return "", err
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取 FTP 应答失败: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// transferResult 读取数据传输结束后的最终应答.
func (c *client) transferResult() (int, string, error) {
	return c.reply()
}

// finish 在数据连接关闭后收尾控制连接上的最终应答.
func (c *client) finish() error {
	_, _, err := c.transferResult()
	return err
}

// Close 关闭控制连接.
func (c *client) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.conn.Close()
}

// openData 打开被动模式数据连接.
func (c *client) openData(ctx context.Context) (net.Conn, error) {
	hosts, port, err := c.passive(ctx)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	var lastErr error
	for _, host := range hosts {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("建立 FTP 数据连接失败: %w", lastErr)
}

// passive 协商被动模式端口, 返回候选主机顺序: 控制连接主机优先, PASV 声明地址兜底.
func (c *client) passive(ctx context.Context) ([]string, string, error) {
	if code, msg, err := c.command(ctx, "EPSV"); err == nil && code == 229 {
		if port, ok := parseEPSV(msg); ok {
			return []string{c.host}, port, nil
		}
	}
	code, msg, err := c.command(ctx, "PASV")
	if err != nil {
		return nil, "", fmt.Errorf("协商 FTP 被动模式失败: %w", err)
	}
	if code != 227 {
		return nil, "", fmt.Errorf("FTP 服务器拒绝被动模式: %d %s", code, msg)
	}
	host, port, ok := parsePASV(msg)
	if !ok {
		return nil, "", fmt.Errorf("FTP 被动模式应答非法: %s", msg)
	}
	hosts := []string{c.host}
	if host != "" && host != c.host {
		hosts = append(hosts, host)
	}
	return hosts, port, nil
}

var pasvPattern = regexp.MustCompile(`\((\d{1,3}),(\d{1,3}),(\d{1,3}),(\d{1,3}),(\d{1,3}),(\d{1,3})\)`)

// parsePASV 解析 227 应答中的地址与端口.
func parsePASV(msg string) (string, string, bool) {
	match := pasvPattern.FindStringSubmatch(msg)
	if match == nil {
		return "", "", false
	}
	parts := make([]int, 6)
	for i := range 6 {
		value, err := strconv.Atoi(match[i+1])
		if err != nil || value < 0 || value > 255 {
			return "", "", false
		}
		parts[i] = value
	}
	host := fmt.Sprintf("%d.%d.%d.%d", parts[0], parts[1], parts[2], parts[3])
	port := parts[4]*256 + parts[5]
	if port == 0 {
		return "", "", false
	}
	return host, strconv.Itoa(port), true
}

// parseEPSV 解析 229 应答中的端口, 格式为 (|||port|).
func parseEPSV(msg string) (string, bool) {
	start := strings.Index(msg, "(")
	end := strings.LastIndex(msg, ")")
	if start < 0 || end <= start {
		return "", false
	}
	parts := strings.Split(msg[start+1:end], "|")
	for _, part := range parts {
		if part == "" {
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		return strconv.Itoa(port), true
	}
	return "", false
}

// stor 通过数据连接写入对象.
func (c *client) stor(ctx context.Context, remote string, body io.Reader) error {
	data, err := c.openData(ctx)
	if err != nil {
		return err
	}
	code, msg, err := c.command(ctx, "STOR %s", remote)
	if err != nil {
		_ = data.Close()
		return fmt.Errorf("上传对象失败: %w", err)
	}
	if code != statusFileOk && code != statusFileOpen {
		_ = data.Close()
		return fmt.Errorf("上传对象失败: %d %s", code, msg)
	}
	_, copyErr := io.Copy(deadlineWriter{conn: data}, body)
	_ = data.Close()
	finalCode, finalMsg, replyErr := c.transferResult()
	switch {
	case copyErr != nil:
		return fmt.Errorf("上传对象失败: %w", copyErr)
	case replyErr != nil:
		return fmt.Errorf("上传对象失败: %w", replyErr)
	case finalCode < 200 || finalCode >= 300:
		return fmt.Errorf("上传对象失败: %d %s", finalCode, finalMsg)
	}
	return nil
}

// deadlineWriter 在每次写入前刷新数据连接截止时间.
type deadlineWriter struct {
	conn net.Conn
}

func (w deadlineWriter) Write(p []byte) (int, error) {
	if err := w.conn.SetDeadline(deadlineNow()); err != nil {
		return 0, err
	}
	return w.conn.Write(p)
}

// deadlineReader 在每次读取前刷新数据连接截止时间.
type deadlineReader struct {
	conn net.Conn
}

func (r deadlineReader) Read(p []byte) (int, error) {
	if err := r.conn.SetDeadline(deadlineNow()); err != nil {
		return 0, err
	}
	return r.conn.Read(p)
}

// stat 查询对象是否存在: SIZE 取体积, MDTM 取修改时间, 不支持时退化为 MLSD.
func (c *client) stat(ctx context.Context, remote string) (storage.ObjectInfo, error) {
	key := storage.NormalizeObjectKey("", strings.TrimPrefix(remote, "/"))
	code, msg, err := c.command(ctx, "SIZE %s", remote)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("查询对象失败: %w", err)
	}
	if code >= 200 && code < 300 {
		size, err := strconv.ParseInt(strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0][3:]), 10, 64)
		if err != nil {
			return storage.ObjectInfo{}, fmt.Errorf("FTP 对象体积应答非法: %s", msg)
		}
		info := storage.ObjectInfo{Key: key, Size: size}
		if code, mdtm, err := c.command(ctx, "MDTM %s", remote); err == nil && code >= 200 && code < 300 {
			if modified := parseMDTM(mdtm); !modified.IsZero() {
				info.ModTime = modified
			}
		}
		return info, nil
	}
	if code != statusFileUnavailable && code != 500 && code != 502 {
		return storage.ObjectInfo{}, fmt.Errorf("查询对象失败: %d %s", code, msg)
	}
	// 部分服务器不支持 SIZE, 或对不存在的对象返回同一应答码: 改为列举父目录确认.
	name := remote[strings.LastIndex(remote, "/")+1:]
	entries, err := c.mlsd(ctx, parentOf(remote))
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("查询对象失败: %d %s", code, msg)
	}
	for _, entry := range entries {
		if entry.name != name {
			continue
		}
		return storage.ObjectInfo{Key: key, Size: entry.size, ModTime: entry.modTime, IsDir: entry.isDir}, nil
	}
	return storage.ObjectInfo{}, storage.ErrNotFound
}

// parseMDTM 解析 213 应答中的 UTC 修改时间.
func parseMDTM(reply string) time.Time {
	value := strings.TrimSpace(strings.SplitN(reply, "\n", 2)[0])
	if len(value) < 3 {
		return time.Time{}
	}
	value = strings.TrimSpace(value[3:])
	if dot := strings.IndexByte(value, '.'); dot > 0 {
		value = value[:dot]
	}
	if len(value) < 14 {
		return time.Time{}
	}
	parsed, err := time.Parse("20060102150405", value[:14])
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// mlsdEntry 是 MLSD 列举返回的单个目录项.
type mlsdEntry struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

// mlsd 列举单个目录下的一级对象.
func (c *client) mlsd(ctx context.Context, remote string) ([]mlsdEntry, error) {
	data, err := c.openData(ctx)
	if err != nil {
		return nil, err
	}
	code, msg, err := c.command(ctx, "MLSD %s", remote)
	if err != nil {
		_ = data.Close()
		return nil, fmt.Errorf("列举目录失败: %w", err)
	}
	if code != statusFileOk && code != statusFileOpen {
		_ = data.Close()
		if code == 500 || code == 502 || code == 504 {
			return nil, fmt.Errorf("FTP 服务器不支持 MLSD 列举 (需要 RFC 3659): %d %s", code, msg)
		}
		return nil, fmt.Errorf("列举目录失败: %d %s", code, msg)
	}
	entries := make([]mlsdEntry, 0, 32)
	scanner := bufio.NewScanner(deadlineReader{conn: data})
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if entry, ok := parseMLSDLine(scanner.Text()); ok {
			entries = append(entries, entry)
		}
	}
	scanErr := scanner.Err()
	_ = data.Close()
	finalCode, finalMsg, replyErr := c.transferResult()
	if scanErr != nil {
		return nil, fmt.Errorf("列举目录失败: %w", scanErr)
	}
	if replyErr != nil {
		return nil, fmt.Errorf("列举目录失败: %w", replyErr)
	}
	if finalCode >= 400 {
		return nil, fmt.Errorf("列举目录失败: %d %s", finalCode, finalMsg)
	}
	return entries, nil
}

// parseMLSDLine 解析一行 MLSD 结果: 分号分隔的 facts, 空格后为名称.
func parseMLSDLine(line string) (mlsdEntry, bool) {
	index := strings.IndexByte(line, ' ')
	if index <= 0 {
		return mlsdEntry{}, false
	}
	facts := line[:index]
	name := line[index+1:]
	if name == "" {
		return mlsdEntry{}, false
	}
	entry := mlsdEntry{name: name}
	category := ""
	for _, fact := range strings.Split(facts, ";") {
		if fact == "" {
			continue
		}
		key, value, found := strings.Cut(fact, "=")
		if !found {
			continue
		}
		switch strings.ToLower(key) {
		case "type":
			category = strings.ToLower(value)
		case "size":
			if size, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				entry.size = size
			}
		case "modify":
			entry.modTime = parseMDTM("213 " + strings.TrimSpace(value))
		}
	}
	switch category {
	case "file":
		return entry, true
	case "dir":
		entry.isDir = true
		return entry, true
	case "cdir", "pdir":
		return mlsdEntry{}, false
	default:
		// 未知类型按文件处理, 避免漏掉后端自定义对象.
		return entry, true
	}
}
