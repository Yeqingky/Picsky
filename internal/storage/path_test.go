package storage

import (
	"strings"
	"testing"
)

func TestRenderPathExamples(t *testing.T) {
	vars := PathVars{
		MD5:    "0f1e2d3c4b5a69788796a5b4c3d2e1f0",
		SHA256: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0",
		SHA512: strings.Repeat("ab", 64),
		Ext:    "png",
	}

	cases := []struct {
		template string
		want     string
	}{
		{"{md5:2}/{md5}.{ext}", "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png"},
		{"{sha256:2}/{sha256:16}/{md5}.{ext}", "0f/0f1e2d3c4b5a6978/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png"},
		{"{md5:4}/{md5:8}.{ext}", "0f1e/0f1e2d3c.png"},
		{"{ext}/{md5}.{ext}", "png/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png"},
		{"{md5}.{ext}", "0f1e2d3c4b5a69788796a5b4c3d2e1f0.png"},
	}
	for _, testCase := range cases {
		got, err := RenderPath(testCase.template, vars)
		if err != nil {
			t.Fatalf("RenderPath(%q) 返回错误: %v", testCase.template, err)
		}
		if got != testCase.want {
			t.Errorf("RenderPath(%q) = %q, 期望 %q", testCase.template, got, testCase.want)
		}
	}
}

func TestRenderPathRandomVariables(t *testing.T) {
	vars := PathVars{MD5: strings.Repeat("a", 32), Ext: "png"}

	first, err := RenderPath("{rand:4}/{md5}.{ext}", vars)
	if err != nil {
		t.Fatalf("RenderPath 返回错误: %v", err)
	}
	second, err := RenderPath("{rand:4}/{md5}.{ext}", vars)
	if err != nil {
		t.Fatalf("RenderPath 返回错误: %v", err)
	}
	if first == second {
		t.Errorf("随机变量应当每次不同: %q", first)
	}
	if !strings.HasSuffix(first, "/"+vars.MD5+".png") {
		t.Errorf("随机变量渲染结果不符合预期: %q", first)
	}

	uuidPath, err := RenderPath("{md5:2}/{uuid}.{ext}", vars)
	if err != nil {
		t.Fatalf("RenderPath 返回错误: %v", err)
	}
	if len(strings.TrimSuffix(uuidPath, ".png")) != 3+36 {
		t.Errorf("UUID 长度不正确: %q", uuidPath)
	}
}

func TestValidateTemplateRejects(t *testing.T) {
	cases := []string{
		"",
		"/{md5}.{ext}",
		"a//b/{md5}.{ext}",
		"../{md5}.{ext}",
		"{ext}/{rand}",
		"fixed/{ext}.png",
		"{year}/{md5}.{ext}",
		"{md5:33}.{ext}",
		"{md5:0}.{ext}",
		"{md5}.{ext",
		strings.Repeat("a", 129) + "/{md5}",
	}
	for _, template := range cases {
		if err := ValidateTemplate(template); err == nil {
			t.Errorf("ValidateTemplate(%q) 应当报错", template)
		}
	}
}

func TestRenderPathMissingVars(t *testing.T) {
	if _, err := RenderPath("{md5}.{ext}", PathVars{Ext: "png"}); err == nil {
		t.Error("缺少 md5 时应当报错")
	}
	if _, err := RenderPath("{md5}.{ext}", PathVars{MD5: strings.Repeat("a", 32)}); err == nil {
		t.Error("缺少 ext 时应当报错")
	}
}

func TestNormalizeObjectKey(t *testing.T) {
	cases := []struct {
		prefix string
		rel    string
		want   string
	}{
		{"image", "0f/foo.png", "image/0f/foo.png"},
		{"/image/", "/0f/foo.png", "image/0f/foo.png"},
		{"", "foo.png", "foo.png"},
		{"image//sub", "foo.png", "image/sub/foo.png"},
		{"image", "", "image"},
	}
	for _, testCase := range cases {
		if got := NormalizeObjectKey(testCase.prefix, testCase.rel); got != testCase.want {
			t.Errorf("NormalizeObjectKey(%q, %q) = %q, 期望 %q", testCase.prefix, testCase.rel, got, testCase.want)
		}
	}
}

func TestJoinURLAndNormalize(t *testing.T) {
	if got := JoinURL("https://cdn.example.com/image/", "0f/foo.png"); got != "https://cdn.example.com/image/0f/foo.png" {
		t.Errorf("JoinURL 结果不正确: %q", got)
	}
	if got := JoinURL("https://cdn.example.com/image", "/0f/foo.png"); got != "https://cdn.example.com/image/0f/foo.png" {
		t.Errorf("JoinURL 结果不正确: %q", got)
	}
	if _, err := NormalizeURLBase("cdn.example.com/image"); err == nil {
		t.Error("缺少协议的分发域名应当报错")
	}
	if got, err := NormalizeURLBase("https://cdn.example.com/image/"); err != nil || got != "https://cdn.example.com/image" {
		t.Errorf("NormalizeURLBase = %q, %v", got, err)
	}
}
