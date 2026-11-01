package service_test

import (
	"testing"
)

func TestSettingsEndpointChangeClearsSecrets(t *testing.T) {
	f := newFixture(t)
	update := func(values map[string]string) {
		t.Helper()
		if err := f.svc.Settings.Update(values); err != nil {
			t.Fatalf("更新设置失败: %v", err)
		}
	}
	assertSecret := func(key, want string) {
		t.Helper()
		got, err := f.svc.Settings.Secret(key)
		if err != nil {
			t.Fatalf("读取秘密设置 %s 失败: %v", key, err)
		}
		if got != want {
			t.Errorf("%s = %q, 期望 %q", key, got, want)
		}
	}

	update(map[string]string{
		"smtp_host":        "smtp.old.example.com",
		"smtp_password":    "smtp-old",
		"captcha_provider": "cap",
		"captcha_endpoint": "https://cap.old.example.com",
		"captcha_secret":   "captcha-old",
	})
	assertSecret("smtp_password", "smtp-old")
	assertSecret("captcha_secret", "captcha-old")

	// 端点未变化时保存其他设置不能误清空密码.
	update(map[string]string{
		"smtp_host":        "smtp.old.example.com",
		"captcha_endpoint": "https://cap.old.example.com",
		"site_name":        "changed",
	})
	assertSecret("smtp_password", "smtp-old")
	assertSecret("captcha_secret", "captcha-old")

	// 改 SMTP 端点且不提交密码时清空密码, 人机验证秘钥不受影响.
	update(map[string]string{"smtp_host": "smtp.new.example.com"})
	assertSecret("smtp_password", "")
	assertSecret("captcha_secret", "captcha-old")

	// 同时提交新密码时保留新值.
	update(map[string]string{
		"smtp_host":     "smtp.other.example.com",
		"smtp_password": "smtp-new",
	})
	assertSecret("smtp_password", "smtp-new")

	// 提供方变化同样会改变凭据接收方, 未提交秘钥时清空.
	update(map[string]string{"captcha_provider": "turnstile"})
	assertSecret("captcha_secret", "")

	// 端点变化并同时提交新秘钥时保留新值.
	update(map[string]string{
		"captcha_provider": "cap",
		"captcha_endpoint": "https://cap.new.example.com",
		"captcha_secret":   "captcha-new",
	})
	assertSecret("captcha_secret", "captcha-new")

	// SMTP 端口变化也属于端点变化.
	update(map[string]string{"smtp_port": "2525"})
	assertSecret("smtp_password", "")
}
