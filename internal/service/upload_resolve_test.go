package service

import (
	"testing"

	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/storage"
)

func TestResolveRecordURL(t *testing.T) {
	cases := []struct {
		name       string
		defaultURL string
		relPath    string
		put        storage.PutResult
		wantURL    string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "分发域名拼接",
			defaultURL: "https://cdn.example.com/image",
			relPath:    "0f/foo.png",
			wantURL:    "https://cdn.example.com/image/0f/foo.png",
			wantPath:   "0f/foo.png",
		},
		{
			name:       "后端直链",
			defaultURL: "",
			relPath:    "0f/foo.png",
			put:        storage.PutResult{URL: "https://img.example.com/2026/10/30/abc.png"},
			wantURL:    "https://img.example.com/2026/10/30/abc.png",
			wantPath:   "2026/10/30/abc.png",
		},
		{
			name:       "后端直链优先于分发域名",
			defaultURL: "https://cdn.example.com/image",
			relPath:    "0f/foo.png",
			put:        storage.PutResult{URL: "https://img.example.com/a/b.png"},
			wantURL:    "https://img.example.com/a/b.png",
			wantPath:   "a/b.png",
		},
		{
			name:       "后端直链指定对象键",
			defaultURL: "",
			relPath:    "picsky/0f/foo.png",
			put:        storage.PutResult{URL: "https://img.example.com/f/abc/foo.png", ObjectKey: "picsky/0f/foo.png"},
			wantURL:    "https://img.example.com/f/abc/foo.png",
			wantPath:   "picsky/0f/foo.png",
		},
		{
			name:    "后端直链非法",
			put:     storage.PutResult{URL: "https:///a.png"},
			wantErr: true,
		},
		{
			name:    "缺少直链与分发域名",
			wantErr: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotURL, gotPath, err := resolveRecordURL(testCase.defaultURL, testCase.relPath, testCase.put)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("期望返回错误, 实际 %q %q", gotURL, gotPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRecordURL 失败: %v", err)
			}
			if gotURL != testCase.wantURL || gotPath != testCase.wantPath {
				t.Errorf("resolveRecordURL = %q, %q, 期望 %q, %q", gotURL, gotPath, testCase.wantURL, testCase.wantPath)
			}
		})
	}
}

func TestCloudreveStoragePrefixAndRecordPath(t *testing.T) {
	policy := &model.StoragePolicy{
		Type:         "cloudreve",
		Config:       `{"directory":"/picsky"}`,
		PathTemplate: "{md5:2}/{md5}.{ext}",
	}
	prefix := storagePrefix(policy)
	if prefix != "picsky" {
		t.Fatalf("存储前缀 = %q", prefix)
	}
	objectKey := storage.WithPrefix(prefix, "ab/foo.png")
	if objectKey != "picsky/ab/foo.png" {
		t.Fatalf("对象键 = %q", objectKey)
	}
	if got := recordPathForPolicy(policy, objectKey); got != "ab/foo.png" {
		t.Fatalf("rel_path = %q", got)
	}

	legacy := &model.StoragePolicy{
		Type:         "cloudreve",
		Config:       `{"directory":"/picsky"}`,
		PathTemplate: "picsky/{md5:2}/{md5}.{ext}",
	}
	if got := recordPathForPolicy(legacy, "picsky/ab/foo.png"); got != "picsky/ab/foo.png" {
		t.Fatalf("旧模板 rel_path = %q", got)
	}
}
