// Package hashing 提供单遍流式哈希计算: 一次读取同时得到 md5, sha1, sha256, sha512 与字节数.
package hashing

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
)

// Result 是单遍扫描结果.
type Result struct {
	MD5    string
	SHA1   string
	SHA256 string
	SHA512 string
	Size   int64
}

// Hasher 是 io.Writer, 把写入的数据同时喂给全部哈希.
type Hasher struct {
	md5    hash.Hash
	sha1   hash.Hash
	sha256 hash.Hash
	sha512 hash.Hash
	size   int64
}

// New 创建 Hasher.
func New() *Hasher {
	return &Hasher{
		md5:    md5.New(),
		sha1:   sha1.New(),
		sha256: sha256.New(),
		sha512: sha512.New(),
	}
}

// Write 实现 io.Writer.
func (h *Hasher) Write(p []byte) (int, error) {
	h.md5.Write(p)
	h.sha1.Write(p)
	h.sha256.Write(p)
	h.sha512.Write(p)
	h.size += int64(len(p))
	return len(p), nil
}

// Result 返回十六进制哈希与总字节数.
func (h *Hasher) Result() Result {
	return Result{
		MD5:    hex.EncodeToString(h.md5.Sum(nil)),
		SHA1:   hex.EncodeToString(h.sha1.Sum(nil)),
		SHA256: hex.EncodeToString(h.sha256.Sum(nil)),
		SHA512: hex.EncodeToString(h.sha512.Sum(nil)),
		Size:   h.size,
	}
}
