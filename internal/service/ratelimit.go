package service

import (
	"sync"
	"time"
)

// RateLimiter 是按 key 的固定窗口计数限流器.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rateWindow
	window  time.Duration
}

type rateWindow struct {
	count int
	start time.Time
}

// NewRateLimiter 创建限流器.
func NewRateLimiter(window time.Duration) *RateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{windows: map[string]*rateWindow{}, window: window}
}

// Allow 判断是否放行; limit <= 0 表示不限. 超限时返回需要等待的时长.
func (r *RateLimiter) Allow(key string, limit int) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.windows) > 10000 {
		r.sweep(now)
	}
	window, ok := r.windows[key]
	if !ok || now.Sub(window.start) >= r.window {
		r.windows[key] = &rateWindow{count: 1, start: now}
		return true, 0
	}
	if window.count >= limit {
		return false, r.window - now.Sub(window.start)
	}
	window.count++
	return true, 0
}

// Count 返回当前窗口内的计数, 不改变计数.
func (r *RateLimiter) Count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	window, ok := r.windows[key]
	if !ok || time.Since(window.start) >= r.window {
		return 0
	}
	return window.count
}

// Add 增加一次计数 (用于失败尝试计数), 返回新计数与窗口剩余时长.
func (r *RateLimiter) Add(key string) (int, time.Duration) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.windows) > 10000 {
		r.sweep(now)
	}
	window, ok := r.windows[key]
	if !ok || now.Sub(window.start) >= r.window {
		r.windows[key] = &rateWindow{count: 1, start: now}
		return 1, r.window
	}
	window.count++
	return window.count, r.window - now.Sub(window.start)
}

// Reset 清除某个键的窗口.
func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.windows, key)
}

// sweep 清理过期窗口.
func (r *RateLimiter) sweep(now time.Time) {
	for key, window := range r.windows {
		if now.Sub(window.start) >= r.window {
			delete(r.windows, key)
		}
	}
}
