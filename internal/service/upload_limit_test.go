package service

import (
	"errors"
	"testing"

	"github.com/yeqingky/Picsky/internal/apperr"
)

func TestCheckPolicyFileSizeLimit(t *testing.T) {
	const limit = int64(50 << 20)
	tests := []struct {
		name     string
		limit    int64
		size     int64
		wantMsg  string
		wantFail bool
	}{
		{name: "未超过策略上限", limit: limit, size: limit},
		{name: "策略不限制", limit: 0, size: 1 << 40},
		{name: "超过策略上限", limit: limit, size: limit + 1, wantMsg: "此存储策略最大支持50MB", wantFail: true},
		{name: "非整数 MB", limit: 1<<20 + 512<<10, size: 1<<20 + 512<<10 + 1, wantMsg: "此存储策略最大支持1.5MB", wantFail: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := checkPolicyFileSizeLimit(testCase.limit, testCase.size)
			if !testCase.wantFail {
				if err != nil {
					t.Fatalf("期望通过, 实际 %v", err)
				}
				return
			}
			var apiErr *apperr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("期望 API 错误, 实际 %v", err)
			}
			if apiErr.Code != "file_too_large" {
				t.Errorf("错误码 = %q, 期望 file_too_large", apiErr.Code)
			}
			if apiErr.Message != testCase.wantMsg {
				t.Errorf("错误消息 = %q, 期望 %q", apiErr.Message, testCase.wantMsg)
			}
			if apiErr.Details["scope"] != "policy" {
				t.Errorf("scope = %v, 期望 policy", apiErr.Details["scope"])
			}
		})
	}
}
