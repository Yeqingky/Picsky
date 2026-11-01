package service

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterRejectsNewKeysAtCapacity(t *testing.T) {
	limiter := NewRateLimiter(time.Hour)
	for i := range maxRateLimitKeys {
		allowed, _ := limiter.Allow(fmt.Sprintf("key-%d", i), 10)
		if !allowed {
			t.Fatalf("容量内新增键应放行, 第 %d 个", i)
		}
	}
	if allowed, _ := limiter.Allow("overflow", 10); allowed {
		t.Error("表满且无过期项时应拒绝新键")
	}
	if got := len(limiter.windows); got != maxRateLimitKeys {
		t.Errorf("拒绝新键后表大小不应增长: %d", got)
	}
	if allowed, _ := limiter.Allow("key-0", 10); !allowed {
		t.Error("已有键在容量满时仍应按计数放行")
	}
}

func TestPasswordResetStopsAddingEmailKeysAfterIPLimit(t *testing.T) {
	auth := &Auth{passwordResetLimiter: NewRateLimiter(time.Hour)}
	ip := "203.0.113.9"
	for i := range passwordResetRequestLimit {
		if !auth.allowPasswordReset(ip, fmt.Sprintf("user-%d@example.com", i)) {
			t.Fatalf("IP 限流窗口内第 %d 次应放行", i+1)
		}
	}
	if auth.allowPasswordReset(ip, "blocked@example.com") {
		t.Fatal("IP 超限后应拒绝")
	}
	for key := range auth.passwordResetLimiter.windows {
		if strings.Contains(key, "blocked@example.com") {
			t.Errorf("IP 超限后不应插入邮箱限流键: %s", key)
		}
	}
	if got := len(auth.passwordResetLimiter.windows); got != passwordResetRequestLimit+1 {
		t.Errorf("限流键数量 = %d, 期望 IP 键加 %d 个邮箱键", got, passwordResetRequestLimit)
	}
}
