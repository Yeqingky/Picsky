package spool

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpoolMemoryOnly(t *testing.T) {
	buffer := New(t.TempDir(), 1024)
	defer buffer.Close()

	if _, err := buffer.Write([]byte("hello")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if buffer.OnDisk() {
		t.Error("小于阈值时不应落盘")
	}
	if buffer.Size() != 5 {
		t.Errorf("Size = %d", buffer.Size())
	}
	reader, err := buffer.Reader()
	if err != nil {
		t.Fatalf("Reader 失败: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("内容不一致: %q", data)
	}
}

func TestSpoolSpillsToTempFile(t *testing.T) {
	tempDir := t.TempDir()
	buffer := New(tempDir, 4)
	defer buffer.Close()

	payload := []byte("0123456789abcdef")
	if _, err := buffer.Write(payload); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if !buffer.OnDisk() {
		t.Fatal("超过阈值时应落盘")
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), TempFilePrefix) {
		t.Fatalf("临时文件命名不符合预期: %+v", entries)
	}

	reader, err := buffer.Reader()
	if err != nil {
		t.Fatalf("Reader 失败: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("落盘后内容不一致: %q", data)
	}
	if err := buffer.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	remaining, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("Close 后临时文件未删除: %+v", remaining)
	}
}

func TestCleanupStale(t *testing.T) {
	tempDir := t.TempDir()
	keep := filepath.Join(tempDir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	stale := filepath.Join(tempDir, TempFilePrefix+"stale")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}

	removed, err := CleanupStale(tempDir)
	if err != nil {
		t.Fatalf("CleanupStale 失败: %v", err)
	}
	if removed != 1 {
		t.Errorf("清理数量 = %d, 期望 1", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("残留临时文件未被删除")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("非 picsky- 前缀的文件不应被删除")
	}
}

func TestCleanupStaleWithoutDirectoryPreservesSharedTempFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	otherFile := filepath.Join(tempDir, TempFilePrefix+"other-process")
	if err := os.WriteFile(otherFile, []byte("keep"), 0o600); err != nil {
		t.Fatalf("准备共享临时文件失败: %v", err)
	}

	removed, err := CleanupStale("")
	if err != nil {
		t.Fatalf("未配置临时目录时清理不应失败: %v", err)
	}
	if removed != 0 {
		t.Errorf("未配置临时目录时应跳过清理, 删除数 = %d", removed)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Errorf("共享系统临时目录中的文件不应被清理: %v", err)
	}
}
