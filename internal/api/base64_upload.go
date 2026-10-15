package api

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/service"
)

const (
	maxBase64JSONMetadataBytes = 64 << 10
	maxBase64JSONPrefixBytes   = maxBase64JSONMetadataBytes / 2
	maxBase64JSONTailBytes     = maxBase64JSONMetadataBytes - maxBase64JSONPrefixBytes
	maxBase64DataURLHeader     = 4096
	base64JSONBodyOverhead     = maxBase64JSONMetadataBytes + maxBase64DataURLHeader
)

var errBase64JSONMetadataTooLarge = errors.New("base64 JSON 元数据超出限制")

type base64UploadPayload struct {
	Filename string
	PolicyID *int64
	Spooled  *service.SpooledUpload
}

type base64UploadMetadata struct {
	Filename string          `json:"filename"`
	PolicyID *int64          `json:"policy_id"`
	Data     json.RawMessage `json:"data"`
}

type base64PrefixReader struct {
	reader    io.Reader
	remaining int64
	unlimited bool
}

func (r *base64PrefixReader) Read(p []byte) (int, error) {
	if r.unlimited {
		return r.reader.Read(p)
	}
	if r.remaining <= 0 {
		return 0, errBase64JSONMetadataTooLarge
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

// readBase64UploadPayload streams the JSON data string directly into the configured upload spool.
func readBase64UploadPayload(body io.Reader, tempDir string, memLimit, limit int64) (*base64UploadPayload, error) {
	prefix := &base64PrefixReader{reader: body, remaining: maxBase64JSONPrefixBytes}
	decoder := json.NewDecoder(prefix)
	metadata := &base64UploadMetadata{}
	token, err := decoder.Token()
	if err != nil {
		return nil, invalidBase64JSON(err)
	}
	if token == nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少 data 字段")
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, invalidBase64JSON(errors.New("请求体必须是 JSON 对象"))
	}

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalidBase64JSON(err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalidBase64JSON(errors.New("JSON 对象字段名必须是字符串"))
		}
		if strings.EqualFold(key, "data") {
			prefix.unlimited = true
			reader := bufio.NewReader(io.MultiReader(decoder.Buffered(), prefix))
			if err := skipBase64JSONWhitespace(reader); err != nil {
				return nil, invalidBase64JSON(err)
			}
			colon, err := reader.ReadByte()
			if err != nil || colon != ':' {
				return nil, invalidBase64JSON(strconv.ErrSyntax)
			}
			spooled, err := readBase64DataValue(reader, tempDir, memLimit, limit)
			if err != nil {
				return nil, err
			}
			if spooled == nil {
				return nil, apperr.ErrInvalidRequest.WithMessage("缺少 data 字段")
			}
			payload := &base64UploadPayload{Spooled: spooled}
			if err := readBase64JSONTail(reader, metadata); err != nil {
				_ = spooled.Spool.Close()
				return nil, err
			}
			if len(metadata.Data) != 0 {
				_ = spooled.Spool.Close()
				return nil, apperr.ErrInvalidRequest.WithMessage("data 字段不能重复")
			}
			payload.Filename = metadata.Filename
			payload.PolicyID = metadata.PolicyID
			payload.Spooled.Filename = metadata.Filename
			return payload, nil
		}

		switch {
		case strings.EqualFold(key, "filename"):
			if err := decoder.Decode(&metadata.Filename); err != nil {
				return nil, invalidBase64JSON(err)
			}
		case strings.EqualFold(key, "policy_id"):
			if err := decoder.Decode(&metadata.PolicyID); err != nil {
				return nil, invalidBase64JSON(err)
			}
		default:
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, invalidBase64JSON(err)
			}
		}
	}
	return nil, apperr.ErrInvalidRequest.WithMessage("缺少 data 字段")
}

func base64UploadRequestBodyLimit(decodedLimit int64) int64 {
	encodedLimit := decodedLimit / 3 * 4
	if decodedLimit%3 != 0 {
		encodedLimit += 4
	}
	return encodedLimit + base64JSONBodyOverhead
}
func invalidBase64JSON(err error) error {
	return apperr.ErrInvalidRequest.WithMessage("请求体解析失败: " + err.Error())
}

func readBase64DataValue(reader *bufio.Reader, tempDir string, memLimit, limit int64) (*service.SpooledUpload, error) {
	if err := skipBase64JSONWhitespace(reader); err != nil {
		return nil, invalidBase64JSON(err)
	}
	value, err := reader.Peek(1)
	if err != nil {
		return nil, invalidBase64JSON(err)
	}
	if value[0] == 'n' {
		if err := readBase64JSONLiteral(reader, "null"); err != nil {
			return nil, invalidBase64JSON(err)
		}
		return nil, nil
	}
	if value[0] != '"' {
		return nil, apperr.ErrInvalidRequest.WithMessage("data 字段必须是字符串")
	}
	jsonValue, err := newBase64JSONStringReader(reader)
	if err != nil {
		return nil, invalidBase64JSON(err)
	}
	encoded, err := newBase64UploadDataReader(jsonValue)
	if err != nil {
		return nil, err
	}
	spooled, err := service.SpoolReader(tempDir, memLimit, encoded, "", limit)
	if err != nil {
		return nil, err
	}
	if spooled.Truncated {
		size := spooled.Spool.Size()
		_ = spooled.Spool.Close()
		return nil, apperr.ErrFileTooLarge.WithDetails(map[string]any{"size": size, "limit": limit})
	}
	if spooled.Spool.Size() == 0 {
		_ = spooled.Spool.Close()
		return nil, apperr.ErrInvalidRequest.WithMessage("缺少 data 字段")
	}
	if err := encoded.finish(); err != nil {
		_ = spooled.Spool.Close()
		return nil, apperr.ErrInvalidRequest.WithMessage("base64 解码失败: " + err.Error())
	}
	return spooled, nil
}

func readBase64JSONTail(reader *bufio.Reader, metadata *base64UploadMetadata) error {
	if err := skipBase64JSONWhitespace(reader); err != nil {
		return invalidBase64JSON(err)
	}
	delimiter, err := reader.ReadByte()
	if err != nil {
		return invalidBase64JSON(err)
	}
	if delimiter == '}' {
		return ensureBase64JSONEOF(reader)
	}
	if delimiter != ',' {
		return invalidBase64JSON(errors.New("JSON 对象未闭合"))
	}
	if err := skipBase64JSONWhitespace(reader); err != nil {
		return invalidBase64JSON(err)
	}
	if next, err := reader.Peek(1); err != nil || next[0] == '}' {
		return invalidBase64JSON(errors.New("JSON 对象尾随逗号"))
	}

	tail := &io.LimitedReader{
		R: io.MultiReader(strings.NewReader("{"), reader),
		N: maxBase64JSONTailBytes,
	}
	decoder := json.NewDecoder(tail)
	if err := decoder.Decode(metadata); err != nil {
		if tail.N == 0 {
			return apperr.ErrInvalidRequest.WithMessage(errBase64JSONMetadataTooLarge.Error())
		}
		return invalidBase64JSON(err)
	}
	if tail.N == 0 {
		return apperr.ErrInvalidRequest.WithMessage(errBase64JSONMetadataTooLarge.Error())
	}
	trailing := bufio.NewReader(io.MultiReader(decoder.Buffered(), tail))
	return ensureBase64JSONEOF(trailing)
}

func ensureBase64JSONEOF(reader *bufio.Reader) error {
	if err := skipBase64JSONWhitespace(reader); err != nil {
		return invalidBase64JSON(err)
	}
	if _, err := reader.Peek(1); err == nil {
		return apperr.ErrInvalidRequest.WithMessage("请求体包含多余内容")
	} else if err != io.EOF {
		return invalidBase64JSON(err)
	}
	return nil
}

func skipBase64JSONWhitespace(reader *bufio.Reader) error {
	for {
		value, err := reader.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch value[0] {
		case ' ', '\t', '\r', '\n':
			_, _ = reader.ReadByte()
		default:
			return nil
		}
	}
}

func readBase64JSONLiteral(reader *bufio.Reader, literal string) error {
	for i := range len(literal) {
		value, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if value != literal[i] {
			return strconv.ErrSyntax
		}
	}
	return nil
}

func newBase64JSONStringReader(reader *bufio.Reader) (*base64JSONStringReader, error) {
	value, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if value != '"' {
		return nil, strconv.ErrSyntax
	}
	return &base64JSONStringReader{reader: reader}, nil
}

type base64JSONStringReader struct {
	reader     *bufio.Reader
	pending    [utf8.UTFMax]byte
	pendingLen int
	pendingOff int
	finished   bool
	err        error
}

func (r *base64JSONStringReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(dst) {
		if r.pendingOff < r.pendingLen {
			n := copy(dst[written:], r.pending[r.pendingOff:r.pendingLen])
			written += n
			r.pendingOff += n
			if r.pendingOff == r.pendingLen {
				r.pendingOff, r.pendingLen = 0, 0
			}
			continue
		}
		if r.err != nil {
			if written > 0 {
				return written, nil
			}
			return 0, r.err
		}
		if r.finished {
			if written > 0 {
				return written, nil
			}
			return 0, io.EOF
		}
		if err := r.readNext(); err != nil {
			if err == io.EOF {
				r.finished = true
			} else {
				r.err = err
			}
		}
	}
	return written, nil
}

func (r *base64JSONStringReader) readNext() error {
	value, err := r.reader.ReadByte()
	if err != nil {
		return io.ErrUnexpectedEOF
	}
	switch {
	case value == '"':
		return io.EOF
	case value == '\\':
		escaped, err := r.reader.ReadByte()
		if err != nil {
			return io.ErrUnexpectedEOF
		}
		switch escaped {
		case '"', '\\', '/':
			r.pending[0], r.pendingLen = escaped, 1
		case 'b':
			r.pending[0], r.pendingLen = '\b', 1
		case 'f':
			r.pending[0], r.pendingLen = '\f', 1
		case 'n':
			r.pending[0], r.pendingLen = '\n', 1
		case 'r':
			r.pending[0], r.pendingLen = '\r', 1
		case 't':
			r.pending[0], r.pendingLen = '\t', 1
		case 'u':
			codeUnit, err := r.readHexRune()
			if err != nil {
				return err
			}
			runeValue := rune(codeUnit)
			if codeUnit >= 0xd800 && codeUnit <= 0xdbff {
				if next, err := r.reader.Peek(2); err == nil && next[0] == '\\' && next[1] == 'u' {
					pair, err := r.reader.Peek(6)
					if err != nil {
						return io.ErrUnexpectedEOF
					}
					low, valid := parseBase64JSONHexRune(pair[2:6])
					if !valid {
						return strconv.ErrSyntax
					}
					if low >= 0xdc00 && low <= 0xdfff {
						_, _ = r.reader.Discard(6)
						runeValue = 0x10000 + (runeValue-0xd800)<<10 + rune(low-0xdc00)
					} else {
						runeValue = utf8.RuneError
					}
				} else {
					runeValue = utf8.RuneError
				}
			} else if codeUnit >= 0xdc00 && codeUnit <= 0xdfff {
				runeValue = utf8.RuneError
			}
			r.pendingLen = utf8.EncodeRune(r.pending[:], runeValue)
		default:
			return strconv.ErrSyntax
		}
	default:
		if value < 0x20 {
			return strconv.ErrSyntax
		}
		r.pending[0], r.pendingLen = value, 1
	}
	return nil
}

func (r *base64JSONStringReader) readHexRune() (uint16, error) {
	var encoded [4]byte
	if _, err := io.ReadFull(r.reader, encoded[:]); err != nil {
		return 0, io.ErrUnexpectedEOF
	}
	value, valid := parseBase64JSONHexRune(encoded[:])
	if !valid {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseBase64JSONHexRune(value []byte) (uint16, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var result uint16
	for _, char := range value {
		result <<= 4
		switch {
		case char >= '0' && char <= '9':
			result |= uint16(char - '0')
		case char >= 'a' && char <= 'f':
			result |= uint16(char-'a') + 10
		case char >= 'A' && char <= 'F':
			result |= uint16(char-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}

type base64UploadDataReader struct {
	decoder    io.Reader
	normalizer *base64Normalizer
}

func (r *base64UploadDataReader) Read(dst []byte) (int, error) {
	return r.decoder.Read(dst)
}

func (r *base64UploadDataReader) finish() error {
	var scratch [512]byte
	for {
		n, err := r.normalizer.Read(scratch[:])
		if n > 0 {
			return strconv.ErrSyntax
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func newBase64UploadDataReader(value *base64JSONStringReader) (*base64UploadDataReader, error) {
	reader := bufio.NewReader(value)
	if err := skipLeadingBase64Whitespace(reader); err != nil {
		return nil, apperr.ErrInvalidRequest.WithMessage("base64 解码失败: " + err.Error())
	}
	if prefix, err := reader.Peek(5); err == nil && string(prefix) == "data:" {
		_, _ = reader.Discard(5)
		header := make([]byte, 0, 128)
		for {
			char, err := reader.ReadByte()
			if err != nil {
				return nil, apperr.ErrInvalidRequest.WithMessage("data URL 格式非法")
			}
			if char == ',' {
				break
			}
			if len(header) >= maxBase64DataURLHeader {
				return nil, apperr.ErrInvalidRequest.WithMessage("data URL 头过长")
			}
			header = append(header, char)
		}
		if !strings.Contains(string(header), ";base64") {
			return nil, apperr.ErrInvalidRequest.WithMessage("data URL 必须是 base64 编码")
		}
	}
	normalizer := &base64Normalizer{reader: reader}
	return &base64UploadDataReader{
		decoder:    base64.NewDecoder(base64.StdEncoding, normalizer),
		normalizer: normalizer,
	}, nil
}

func skipLeadingBase64Whitespace(reader *bufio.Reader) error {
	for {
		runeValue, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if unicode.IsSpace(runeValue) {
			continue
		}
		if err := reader.UnreadRune(); err != nil {
			return err
		}
		return nil
	}
}

type base64Normalizer struct {
	reader       *bufio.Reader
	mode         byte
	modulo       int
	padded       bool
	paddingCount int
	pendingSpace bool
	finalized    bool
	paddingLeft  int
	readErr      error
}

func (r *base64Normalizer) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if r.readErr != nil {
		return 0, r.readErr
	}
	written := 0
	for written < len(dst) {
		if r.paddingLeft > 0 {
			dst[written] = '='
			written++
			r.paddingLeft--
			continue
		}
		if r.finalized {
			if written > 0 {
				return written, nil
			}
			return 0, io.EOF
		}
		char, err := r.nextByte()
		if err == io.EOF {
			r.finalized = true
			if r.padded {
				if r.mode == 'u' || r.modulo != 0 {
					err = strconv.ErrSyntax
				} else {
					err = io.EOF
				}
			} else if r.modulo == 1 || (r.mode == 's' && r.modulo != 0) {
				err = strconv.ErrSyntax
			} else {
				r.paddingLeft = (4 - r.modulo) % 4
				err = nil
			}
			if err != nil {
				if written > 0 {
					r.readErr = err
					return written, nil
				}
				return 0, err
			}
			continue
		}
		if err != nil {
			if written > 0 {
				r.readErr = err
				return written, nil
			}
			return 0, err
		}
		dst[written] = char
		written++
	}
	return written, nil
}

func (r *base64Normalizer) nextByte() (byte, error) {
	for {
		runeValue, size, err := r.reader.ReadRune()
		if err != nil {
			return 0, err
		}
		if unicode.IsSpace(runeValue) {
			if runeValue == '\r' || runeValue == '\n' {
				continue
			}
			r.pendingSpace = true
			continue
		}
		if r.pendingSpace || size != 1 || runeValue > 0x7f {
			return 0, strconv.ErrSyntax
		}
		if r.padded && runeValue != '=' {
			return 0, strconv.ErrSyntax
		}
		char := byte(runeValue)
		switch {
		case char == '+' || char == '/':
			if r.mode == 'u' {
				return 0, strconv.ErrSyntax
			}
			r.mode = 's'
		case char == '-' || char == '_':
			if r.mode == 's' {
				return 0, strconv.ErrSyntax
			}
			r.mode = 'u'
			if char == '-' {
				char = '+'
			} else {
				char = '/'
			}
		case char == '=':
			if r.mode == 'u' || r.paddingCount >= 2 {
				return 0, strconv.ErrSyntax
			}
			r.padded = true
			r.paddingCount++
		default:
			if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
				return 0, strconv.ErrSyntax
			}
		}
		r.modulo = (r.modulo + 1) % 4
		return char, nil
	}
}
