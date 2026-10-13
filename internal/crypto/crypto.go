// Package crypto 提供凭据的对称加密: AES-256-GCM, 主密钥来自环境变量, 不落库.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher 用主密钥包装 AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// New 用 32 字节主密钥构造 Cipher.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("主密钥长度必须为 32 字节, 当前 %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文, 返回 base64(nonce||密文). 空串返回空串.
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Decrypt 解密 Encrypt 的产物. 空串返回空串.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文不是合法 base64: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", errors.New("密文长度不足")
	}
	nonce, body := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", errors.New("密文解密失败: 主密钥不匹配或数据被篡改")
	}
	return string(plain), nil
}

// RandomToken 生成 n 字节随机数据的 base64url 文本, 用于密钥与会话.
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// RandomBytes 用随机字节填充缓冲区.
func RandomBytes(buf []byte) error {
	_, err := io.ReadFull(rand.Reader, buf)
	return err
}
