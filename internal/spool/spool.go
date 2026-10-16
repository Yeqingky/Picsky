// Package spool 提供可重读的上传缓冲: 小文件驻留内存, 超过阈值落到临时文件.
// 临时文件一律带 picsky- 前缀, 上传结束立即删除; 仅显式配置的临时目录会在启动时扫描残留.
package spool

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TempFilePrefix 是临时文件前缀.
const TempFilePrefix = "picsky-"

// Spool 是可重读的写入缓冲.
type Spool struct {
	mu          sync.Mutex
	memLimit    int64
	tempDirPath string
	mem         bytes.Buffer
	file        *os.File
	path        string
	size        int64
	closed      bool
}

// New 创建 Spool; tempDir 为空时使用系统临时目录.
func New(tempDir string, memLimit int64) *Spool {
	if memLimit < 0 {
		memLimit = 0
	}
	if strings.TrimSpace(tempDir) == "" {
		tempDir = os.TempDir()
	}
	return &Spool{memLimit: memLimit, tempDirPath: tempDir}
}

// Write 实现 io.Writer.
func (s *Spool) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("spool: 已关闭")
	}
	if s.file == nil && s.size+int64(len(p)) > s.memLimit {
		if err := s.spill(); err != nil {
			return 0, err
		}
	}
	var (
		n   int
		err error
	)
	if s.file != nil {
		n, err = s.file.Write(p)
	} else {
		n, err = s.mem.Write(p)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("spool: 写入失败: %w", err)
	}
	return n, nil
}

// spill 把内存缓冲落到临时文件.
func (s *Spool) spill() error {
	file, err := os.CreateTemp(s.tempDirPath, TempFilePrefix+"*")
	if err != nil {
		return fmt.Errorf("spool: 创建临时文件失败: %w", err)
	}
	if s.mem.Len() > 0 {
		if _, err := file.Write(s.mem.Bytes()); err != nil {
			file.Close()
			os.Remove(file.Name())
			return fmt.Errorf("spool: 写入临时文件失败: %w", err)
		}
		s.mem.Reset()
	}
	s.file = file
	s.path = file.Name()
	return nil
}

// Size 返回已写入字节数.
func (s *Spool) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// OnDisk 表示内容是否已落在临时文件.
func (s *Spool) OnDisk() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file != nil
}

// Reader 返回从头开始的可寻址读取器.
func (s *Spool) Reader() (io.ReadSeeker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("spool: 已关闭")
	}
	if s.file != nil {
		if _, err := s.file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("spool: 定位失败: %w", err)
		}
		return s.file, nil
	}
	return bytes.NewReader(s.mem.Bytes()), nil
}

// OpenReader 返回独立的只读句柄, 用于并发读取 (如尺寸解析).
func (s *Spool) OpenReader() (io.ReadSeekCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("spool: 已关闭")
	}
	if s.file != nil {
		handle, err := os.Open(s.path)
		if err != nil {
			return nil, fmt.Errorf("spool: 打开临时文件失败: %w", err)
		}
		return handle, nil
	}
	return nopReadSeekCloser{bytes.NewReader(s.mem.Bytes())}, nil
}

// Close 关闭并删除临时文件.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.mem.Reset()
	if s.file != nil {
		name := s.file.Name()
		if err := s.file.Close(); err != nil {
			os.Remove(name)
			return err
		}
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.file = nil
	s.path = ""
	return nil
}

type nopReadSeekCloser struct{ io.ReadSeeker }

func (nopReadSeekCloser) Close() error { return nil }

// CleanupStale 清理显式配置的应用临时目录中的 picsky- 残留文件.
func CleanupStale(tempDir string) (int, error) {
	if strings.TrimSpace(tempDir) == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("扫描临时目录失败: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), TempFilePrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(tempDir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}
