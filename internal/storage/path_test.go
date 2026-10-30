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
		{"{seq}.jpg", "1.jpg"},
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

func TestPathRendererKeepsGeneratedVariablesAcrossSequences(t *testing.T) {
	renderer, err := NewPathRenderer("{rand:8}_{seq}_{rand:8}.jpg", PathVars{})
	if err != nil {
		t.Fatalf("NewPathRenderer 返回错误: %v", err)
	}
	first, err := renderer.Render(1)
	if err != nil {
		t.Fatalf("Render(1) 返回错误: %v", err)
	}
	second, err := renderer.Render(2)
	if err != nil {
		t.Fatalf("Render(2) 返回错误: %v", err)
	}
	firstParts := strings.Split(first, "_")
	secondParts := strings.Split(second, "_")
	if len(firstParts) != 3 || len(secondParts) != 3 {
		t.Fatalf("路径变量分段不符合预期: %q, %q", first, second)
	}
	if firstParts[1] != "1" || secondParts[1] != "2" {
		t.Errorf("递增序号不符合预期: %q, %q", firstParts[1], secondParts[1])
	}
	if firstParts[0] != secondParts[0] || firstParts[2] != secondParts[2] {
		t.Errorf("非序号变量在重试时应保持不变: %q, %q", first, second)
	}

	highNumber, err := renderer.Render(150)
	if err != nil {
		t.Fatalf("Render(150) 返回错误: %v", err)
	}
	if !strings.Contains(highNumber, "_150_") {
		t.Errorf("递增序号应支持超过旧重试次数的值: %q", highNumber)
	}
}

func TestExtractSequence(t *testing.T) {
	cases := []struct {
		template string
		relPath  string
		want     uint64
		ok       bool
	}{
		{"{seq}.{ext}", "930.webp", 930, true},
		{"{seq}.{ext}", "0.png", 0, false},
		{"{seq}.{ext}", "007.png", 0, false},
		{"{seq}.{ext}", "abc.png", 0, false},
		{"{seq}.{ext}", "2/1.png", 0, false},
		{"img/{seq}.{ext}", "img/12.jpg", 12, true},
		{"img/{seq}.{ext}", "other/12.jpg", 0, false},
		{"{md5:2}/{seq}.{ext}", "ab/7.png", 7, true},
		{"{seq}-{rand:8}.{ext}", "42-a2b3c4d5.jpg", 42, true},
		{"{sha256:8}_{seq}.{ext}", "a2b3c4d5_15.png", 15, true},
		{"{md5}.{ext}", "abc.png", 0, false},
	}
	for _, testCase := range cases {
		got, ok := ExtractSequence(testCase.template, testCase.relPath)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("ExtractSequence(%q, %q) = %d, %v; 期望 %d, %v",
				testCase.template, testCase.relPath, got, ok, testCase.want, testCase.ok)
		}
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
		"{seq}_{seq}.jpg",
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

func TestObjectPathFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://img.example.com/a/b/c.png", "a/b/c.png", true},
		{"https://img.example.com//a//b.png?x=1", "a/b.png", true},
		{"https://img.example.com/a%20b/c.png", "a b/c.png", true},
		{"https://img.example.com/", "", false},
		{"https://img.example.com/a/../b.png", "", false},
		{"ftp://img.example.com/a.png", "", false},
		{"img.example.com/a.png", "", false},
	}
	for _, testCase := range cases {
		got, err := ObjectPathFromURL(testCase.url)
		if testCase.ok {
			if err != nil || got != testCase.want {
				t.Errorf("ObjectPathFromURL(%q) = %q, %v, 期望 %q", testCase.url, got, err, testCase.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ObjectPathFromURL(%q) 应返回错误, 实际 %q", testCase.url, got)
		}
	}
}
