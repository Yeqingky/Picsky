// Package web 内嵌前端构建产物.
package web

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:build
var embedded embed.FS

// ErrNotBuilt 表示前端产物尚未构建.
var ErrNotBuilt = errors.New("前端产物尚未构建: 请先执行 cd web && npm install && npm run build")

// FS 返回前端产物文件系统 (build 目录).
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "build")
}

// IndexHTML 返回 index.html 内容.
func IndexHTML() ([]byte, error) {
	return fs.ReadFile(embedded, "build/index.html")
}

// Available 判断前端产物是否存在.
func Available() bool {
	_, err := fs.Stat(embedded, "build/index.html")
	return err == nil
}
