package service

import (
	"context"
	"time"
)

// contextWithTimeout 生成带超时的上下文, 用于所有外部调用, 避免无限等待.
func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}
