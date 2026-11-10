package ftp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeqingky/Picsky/internal/storage"
)

// fakeServer 是仅覆盖驱动所用命令的最小 FTP 服务端.
type fakeServer struct {
	ln     net.Listener
	mu     sync.Mutex
	files  map[string][]byte
	dirs   map[string]bool
	dataLn net.Listener
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	s := &fakeServer{ln: ln, files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve()
	return s
}

func (s *fakeServer) addr() string {
	return s.ln.Addr().String()
}

func (s *fakeServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	write := func(line string) {
		_, _ = io.WriteString(conn, line+"\r\n")
	}
	write("220 fake ftp ready")
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		command, argument, _ := strings.Cut(scanner.Text(), " ")
		switch strings.ToUpper(command) {
		case "USER":
			write("331 password required")
		case "PASS":
			write("230 logged in")
		case "OPTS", "TYPE", "NOOP":
			write("200 ok")
		case "CWD":
			if s.exists(argument) {
				write("250 ok")
			} else {
				write("550 no such directory")
			}
		case "MKD":
			s.mu.Lock()
			if s.dirs[argument] {
				s.mu.Unlock()
				write("550 already exists")
				continue
			}
			s.dirs[argument] = true
			s.mu.Unlock()
			write("257 created")
		case "SIZE":
			s.mu.Lock()
			data, ok := s.files[argument]
			s.mu.Unlock()
			if !ok {
				write("550 unavailable")
				continue
			}
			write(fmt.Sprintf("213 %d", len(data)))
		case "MDTM":
			s.mu.Lock()
			_, ok := s.files[argument]
			s.mu.Unlock()
			if !ok {
				write("550 unavailable")
				continue
			}
			write("213 20260102030405")
		case "DELE":
			s.mu.Lock()
			_, ok := s.files[argument]
			delete(s.files, argument)
			s.mu.Unlock()
			if !ok {
				write("550 unavailable")
				continue
			}
			write("250 deleted")
		case "EPSV":
			write("500 unknown command")
		case "PASV":
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				write("425 cannot open data connection")
				continue
			}
			s.mu.Lock()
			s.dataLn = ln
			s.mu.Unlock()
			port := ln.Addr().(*net.TCPAddr).Port
			write(fmt.Sprintf("227 Entering Passive Mode (127,0,0,1,%d,%d)", port/256, port%256))
		case "STOR":
			write("150 ready")
			data := s.readData()
			s.mu.Lock()
			s.files[argument] = data
			s.mu.Unlock()
			write("226 transferred")
		case "RETR":
			s.mu.Lock()
			data, ok := s.files[argument]
			s.mu.Unlock()
			if !ok {
				write("550 unavailable")
				continue
			}
			write("150 ready")
			s.writeData(data)
			write("226 transferred")
		case "MLSD":
			s.mu.Lock()
			if !s.dirs[argument] {
				s.mu.Unlock()
				write("550 no such directory")
				continue
			}
			listing := s.listingLocked(argument)
			s.mu.Unlock()
			write("150 ready")
			s.writeData([]byte(listing))
			write("226 transferred")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("502 not implemented")
		}
	}
}

func (s *fakeServer) exists(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirs[path]
}

func (s *fakeServer) dataConn() net.Conn {
	s.mu.Lock()
	ln := s.dataLn
	s.mu.Unlock()
	if ln == nil {
		return nil
	}
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	conn, err := ln.Accept()
	if err != nil {
		return nil
	}
	return conn
}

func (s *fakeServer) readData() []byte {
	conn := s.dataConn()
	if conn == nil {
		return nil
	}
	defer conn.Close()
	data, _ := io.ReadAll(conn)
	return data
}

func (s *fakeServer) writeData(data []byte) {
	conn := s.dataConn()
	if conn == nil {
		return
	}
	_, _ = conn.Write(data)
	_ = conn.Close()
}

// listingLocked 生成 MLSD 结果; 调用方必须已持有锁.
func (s *fakeServer) listingLocked(dir string) string {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var names []string
	for path := range s.files {
		if strings.HasPrefix(path, prefix) && !strings.Contains(strings.TrimPrefix(path, prefix), "/") {
			names = append(names, "file\x00"+path)
		}
	}
	for path := range s.dirs {
		if path == "/" || !strings.HasPrefix(path, prefix) {
			continue
		}
		if !strings.Contains(strings.TrimPrefix(path, prefix), "/") {
			names = append(names, "dir\x00"+path)
		}
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, item := range names {
		kind, path, _ := strings.Cut(item, "\x00")
		name := strings.TrimPrefix(path, prefix)
		if kind == "dir" {
			fmt.Fprintf(&sb, "type=dir;modify=20260102030405; %s\r\n", name)
			continue
		}
		fmt.Fprintf(&sb, "type=file;size=%d;modify=20260102030405; %s\r\n", len(s.files[path]), name)
	}
	return sb.String()
}

func newTestAdapter(t *testing.T, s *fakeServer, directory string) storage.Adapter {
	t.Helper()
	adapter, err := storage.New(driverType, storage.Options{
		Config:  map[string]string{"endpoint": s.addr(), "directory": directory},
		Secrets: map[string]string{"username": "picsky", "password": "secret"},
	})
	if err != nil {
		t.Fatalf("实例化驱动失败: %v", err)
	}
	return adapter
}

func TestSpec(t *testing.T) {
	spec, ok := storage.SpecOf(driverType)
	if !ok {
		t.Fatal("驱动未注册")
	}
	if spec.URLFromBackend {
		t.Fatal("FTP 由分发域名拼接直链, 不应声明 url_from_backend")
	}
	caps := spec.Capabilities
	want := []string{"upload", "delete", "overwrite", "stat", "list", "read", "mkdir"}
	if strings.Join(caps, ",") != strings.Join(want, ",") {
		t.Fatalf("能力声明不符: %v", caps)
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		raw      string
		expected string
		ok       bool
	}{
		{"ftp.example.com", "ftp.example.com:21", true},
		{"ftp.example.com:2121", "ftp.example.com:2121", true},
		{"ftp://ftp.example.com", "ftp.example.com:21", true},
		{"ftp://ftp.example.com:21", "ftp.example.com:21", true},
		{"", "", false},
		{"ftps://ftp.example.com", "", false},
		{"ftp.example.com/path", "", false},
		{"2001:db8::1", "", false},
	}
	for _, item := range cases {
		addr, err := parseEndpoint(item.raw)
		if item.ok != (err == nil) {
			t.Fatalf("%q: 错误预期不符, err=%v", item.raw, err)
		}
		if err == nil && addr != item.expected {
			t.Fatalf("%q: 地址不符, got=%s", item.raw, addr)
		}
	}
}

func TestParsePASVAndEPSV(t *testing.T) {
	host, port, ok := parsePASV("227 Entering Passive Mode (185,27,134,11,62,170)")
	if !ok || host != "185.27.134.11" || port != "16042" {
		t.Fatalf("PASV 解析失败: %s %s %v", host, port, ok)
	}
	if _, _, ok := parsePASV("227 unusual"); ok {
		t.Fatal("非法 PASV 应答被接受")
	}
	if port, ok := parseEPSV("229 Entering Extended Passive Mode (|||6446|)"); !ok || port != "6446" {
		t.Fatalf("EPSV 解析失败: %s %v", port, ok)
	}
}

func TestParseMLSDLine(t *testing.T) {
	entry, ok := parseMLSDLine("type=file;size=42;modify=20260102030405; a b.png")
	if !ok || entry.name != "a b.png" || entry.size != 42 || entry.isDir {
		t.Fatalf("MLSD 文件解析失败: %+v", entry)
	}
	if !entry.modTime.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("MLSD 修改时间解析失败: %v", entry.modTime)
	}
	entry, ok = parseMLSDLine("type=dir;modify=20260102030405; nested")
	if !ok || !entry.isDir || entry.name != "nested" {
		t.Fatalf("MLSD 目录解析失败: %+v", entry)
	}
	if _, ok := parseMLSDLine("type=cdir;modify=20260102030405; ."); ok {
		t.Fatal("cdir 不应作为对象返回")
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	server := newFakeServer(t)
	adapter := newTestAdapter(t, server, "/htdocs")

	content := []byte("hello ftp")
	if _, err := adapter.Put(ctx, "ab/cd/pic.png", bytes.NewReader(content), int64(len(content)), "image/png"); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if !server.exists("/htdocs") || !server.exists("/htdocs/ab") || !server.exists("/htdocs/ab/cd") {
		t.Fatal("上传未逐级创建目录")
	}

	info, err := adapter.Stat(ctx, "ab/cd/pic.png")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("对象体积不符: %d", info.Size)
	}
	if info.ModTime.IsZero() {
		t.Fatal("对象修改时间缺失")
	}

	reader, err := adapter.Read(ctx, "ab/cd/pic.png")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取内容失败: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("关闭读取器失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("读取内容不符: %q", got)
	}

	objects, err := adapter.List(ctx, "")
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	if len(objects) != 1 || objects[0].Key != "ab/cd/pic.png" || objects[0].Size != int64(len(content)) {
		t.Fatalf("列举结果不符: %+v", objects)
	}

	if err := adapter.Delete(ctx, "ab/cd/pic.png"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := adapter.Stat(ctx, "ab/cd/pic.png"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("删除后查询应返回 ErrNotFound, got=%v", err)
	}
	if err := adapter.Delete(ctx, "ab/cd/pic.png"); err != nil {
		t.Fatalf("重复删除应视为成功: %v", err)
	}
	if _, err := adapter.Read(ctx, "ab/cd/pic.png"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("读取缺失对象应返回 ErrNotFound, got=%v", err)
	}
}

func TestTestWritesAndCleansProbe(t *testing.T) {
	ctx := context.Background()
	server := newFakeServer(t)
	adapter := newTestAdapter(t, server, "")

	if err := adapter.Test(ctx, "probe"); err != nil {
		t.Fatalf("连接测试失败: %v", err)
	}
	objects, err := adapter.List(ctx, "probe")
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("探测对象未清理: %+v", objects)
	}
	if !server.exists("/probe") {
		t.Fatal("连接测试未创建目录")
	}
}

func TestObjectKeyGuard(t *testing.T) {
	ctx := context.Background()
	server := newFakeServer(t)
	adapter := newTestAdapter(t, server, "/htdocs")
	if _, err := adapter.Put(ctx, "../escape.png", bytes.NewReader(nil), 0, "image/png"); err == nil {
		t.Fatal("对象键包含 .. 时应拒绝")
	}
}

func TestInvalidEndpoint(t *testing.T) {
	_, err := storage.New(driverType, storage.Options{Config: map[string]string{"endpoint": "ftp://"}})
	if err == nil {
		t.Fatal("空主机名应拒绝")
	}
	_, err = storage.New(driverType, storage.Options{
		Config: map[string]string{"endpoint": "ftp.example.com", "directory": "/a/../b"},
	})
	if err == nil {
		t.Fatal("远程目录包含 .. 时应拒绝")
	}
}
