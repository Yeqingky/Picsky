package service_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"
	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/db"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
	"github.com/yeqingky/Picsky/internal/storage"
	"github.com/yeqingky/Picsky/migrations"

	_ "github.com/yeqingky/Picsky/internal/storage/webdav"
)

// fixture 是集成测试夹具: 内存数据库 + 进程内 WebDAV 后端 + 已注册的管理员.
type fixture struct {
	t         *testing.T
	svc       *service.Services
	cfg       *config.Config
	database  *gorm.DB
	davRoot   string
	admin     *service.Principal
	normalGID int64
}

// newFixture 构造夹具.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		SecretKey:         bytes.Repeat([]byte{0x2b}, 32),
		DBDriver:          "sqlite",
		DBDSN:             filepath.Join(dir, "test.db"),
		Listen:            "127.0.0.1:0",
		APIPrefix:         "/api/v1",
		LskyCompatEnabled: true,
		LskyCompatPrefix:  "/lsky",
		TempDir:           filepath.Join(dir, "tmp"),
		UploadMemoryLimit: 1 << 20,
		LogLevel:          "error",
	}
	if err := os.MkdirAll(cfg.TempDir, 0o750); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	database, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close(database) })
	if err := migrations.Run(database); err != nil {
		t.Fatalf("执行迁移失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := service.New(cfg, database, logger)
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}

	davRoot := filepath.Join(dir, "dav")
	if err := os.MkdirAll(davRoot, 0o755); err != nil {
		t.Fatalf("创建 WebDAV 目录失败: %v", err)
	}
	server := httptest.NewServer(&webdav.Handler{
		Prefix:     "/dav",
		FileSystem: webdav.Dir(davRoot),
		LockSystem: webdav.NewMemLS(),
	})
	t.Cleanup(server.Close)

	fixture := &fixture{t: t, svc: svc, cfg: cfg, database: database, davRoot: davRoot}
	fixture.cfg.DBDSN = server.URL + "/dav"

	user, err := svc.Auth.Register(context.Background(), service.RegisterInput{
		Username: "admin",
		Email:    "admin@example.com",
		Password: "password123",
	}, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("注册首个用户失败: %v", err)
	}
	principal, err := svc.Auth.PrincipalFor(user.ID, nil, nil)
	if err != nil {
		t.Fatalf("加载主体失败: %v", err)
	}
	if !principal.Has(model.PermManagePolicies) {
		t.Fatal("首个注册账号应进入管理员组")
	}
	fixture.admin = principal

	groups, err := svc.Groups.List()
	if err != nil {
		t.Fatalf("读取用户组失败: %v", err)
	}
	for _, group := range groups {
		if group.Name == "普通用户" {
			fixture.normalGID = group.ID
		}
	}
	if fixture.normalGID == 0 {
		t.Fatal("未找到默认用户组")
	}
	return fixture
}

// davEndpoint 返回 WebDAV 服务地址.
func (f *fixture) davEndpoint() string { return f.cfg.DBDSN }

// createPolicy 创建策略并返回.
func (f *fixture) createPolicy(name, template, conflict string) *model.StoragePolicy {
	return f.createPolicyWithPrefix(name, template, conflict, "image")
}

// createPolicyWithPrefix 创建指定对象前缀的策略.
func (f *fixture) createPolicyWithPrefix(name, template, conflict, prefix string) *model.StoragePolicy {
	f.t.Helper()
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           name,
		Type:           "webdav",
		Enabled:        new(true),
		ObjectPrefix:   prefix,
		PathTemplate:   template,
		ConflictPolicy: conflict,
		Config:         map[string]string{"endpoint": f.davEndpoint()},
		URLs: []service.URLInput{
			{Alias: "融合CDN", URL: "https://cdn.example.com/image/", IsDefault: true},
			{Alias: "高速CDN", URL: "https://ucdn.example.com/image/"},
		},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		f.t.Fatalf("创建策略失败: %v", err)
	}
	return policy
}

// authorize 把策略加入普通用户组白名单并清空队列任务.
func (f *fixture) authorize(policyID int64) {
	f.t.Helper()
	if err := f.svc.Groups.SetPolicies(f.normalGID, []int64{policyID}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		f.t.Fatalf("授权策略失败: %v", err)
	}
}

// register 注册一个普通用户并返回主体.
func (f *fixture) register(username string) *service.Principal {
	f.t.Helper()
	user, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
		Username: username,
		Email:    username + "@example.com",
		Password: "password123",
	}, "127.0.0.1", "test-agent")
	if err != nil {
		f.t.Fatalf("注册用户失败: %v", err)
	}
	principal, err := f.svc.Auth.PrincipalFor(user.ID, nil, nil)
	if err != nil {
		f.t.Fatalf("加载主体失败: %v", err)
	}
	return principal
}

// upload 执行一次上传.
func (f *fixture) upload(principal *service.Principal, policyID *int64, filename string, content []byte) (*service.UploadResult, error) {
	f.t.Helper()
	spooled, err := service.SpoolReader(f.cfg.TempDir, f.cfg.UploadMemoryLimit, bytes.NewReader(content), filename, 0)
	if err != nil {
		return nil, err
	}
	defer spooled.Spool.Close()
	return f.svc.Upload.Upload(context.Background(), service.UploadRequest{
		Principal: principal,
		PolicyID:  policyID,
		Spooled:   spooled,
		Source:    model.SourceWeb,
		ClientIP:  "127.0.0.1",
		UserAgent: "test-agent",
	})
}

// mustUpload 执行上传并要求成功.
func (f *fixture) mustUpload(principal *service.Principal, policyID *int64, filename string, content []byte) *service.UploadResult {
	f.t.Helper()
	result, err := f.upload(principal, policyID, filename, content)
	if err != nil {
		f.t.Fatalf("上传失败: %v", err)
	}
	return result
}

// pngBytes 生成确定性的 PNG 内容.
func pngBytes(t *testing.T, seed int, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := range width {
		for y := range height {
			canvas.Set(x, y, color.RGBA{R: uint8(x + seed), G: uint8(y*3 + seed), B: uint8(seed * 5), A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	return buffer.Bytes()
}

// recordsFor 统计某策略下的活动记录数.
func (f *fixture) recordsFor(policyID int64) int64 {
	f.t.Helper()
	var count int64
	if err := f.database.Model(&model.Image{}).Where("policy_id = ? AND deleted_at IS NULL", policyID).Count(&count).Error; err != nil {
		f.t.Fatalf("统计记录失败: %v", err)
	}
	return count
}

// reloadUser 重新读取用户.
func (f *fixture) reloadUser(id int64) *model.User {
	f.t.Helper()
	var user model.User
	if err := f.database.First(&user, id).Error; err != nil {
		f.t.Fatalf("读取用户失败: %v", err)
	}
	return &user
}

func TestUploadDedupAndQuota(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5:2}/{md5}.{ext}", model.ConflictRename)
	content := pngBytes(t, 1, 8, 6)

	result := f.mustUpload(f.admin, &policy.ID, "photo.png", content)
	if result.Status != 201 || result.Dedup || !result.QuotaCharged {
		t.Fatalf("首次上传结果不符合预期: %+v", result)
	}
	record := result.Record
	sum := sha256.Sum256(content)
	if record.SHA256 != hex.EncodeToString(sum[:]) {
		t.Error("sha256 与内容不一致")
	}
	if record.Ext != "png" || record.Format != "png" || record.MIME != "image/png" {
		t.Errorf("格式字段不正确: %+v", record)
	}
	if record.Width == nil || *record.Width != 8 || record.Height == nil || *record.Height != 6 {
		t.Errorf("宽高解析不正确: %v x %v", record.Width, record.Height)
	}
	if record.OriginalFilename != "photo.png" {
		t.Errorf("原始文件名不正确: %q", record.OriginalFilename)
	}
	if !strings.HasPrefix(record.RelPath, record.MD5[:2]+"/") || !strings.HasSuffix(record.RelPath, ".png") {
		t.Errorf("魔法路径渲染不符合模板: %q", record.RelPath)
	}
	if record.ObjectKey != "image/"+record.RelPath {
		t.Errorf("对象键未加前缀: %q", record.ObjectKey)
	}
	if record.URL != "https://cdn.example.com/image/"+record.RelPath {
		t.Errorf("默认分发域名拼接不正确: %q", record.URL)
	}
	if len(result.URLs) != 2 || !result.URLs[0].IsDefault {
		t.Errorf("分发域名列表不正确: %+v", result.URLs)
	}
	if _, err := os.Stat(filepath.Join(f.davRoot, filepath.FromSlash(record.ObjectKey))); err != nil {
		t.Fatalf("外部对象未写入: %v", err)
	}

	user := f.reloadUser(f.admin.User.ID)
	if user.UsedImages != 1 || user.UsedBytes != int64(len(content)) {
		t.Errorf("配额计数不正确: images=%d bytes=%d", user.UsedImages, user.UsedBytes)
	}

	second := f.mustUpload(f.admin, &policy.ID, "photo-again.png", content)
	if second.Status != 200 || !second.Dedup {
		t.Fatalf("重复上传应命中秒传: %+v", second)
	}
	if second.Record.ID != record.ID {
		t.Error("同用户重复上传应返回已有记录")
	}
	if f.recordsFor(policy.ID) != 1 {
		t.Errorf("秒传不应新增记录, 当前 %d 条", f.recordsFor(policy.ID))
	}

	check, err := f.svc.Upload.Check(f.admin, record.SHA256, &policy.ID)
	if err != nil {
		t.Fatalf("秒传检查失败: %v", err)
	}
	if !check.Exists || check.Record == nil || check.Record.ID != record.ID {
		t.Errorf("秒传检查结果不正确: %+v", check)
	}
	missing, err := f.svc.Upload.Check(f.admin, strings.Repeat("f", 64), &policy.ID)
	if err != nil {
		t.Fatalf("秒传检查失败: %v", err)
	}
	if missing.Exists {
		t.Error("不存在的 sha256 不应返回 exists")
	}
}

func TestCrossUserReuseIsFree(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5:2}/{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	content := pngBytes(t, 3, 6, 6)

	owner := f.mustUpload(f.admin, &policy.ID, "shared.png", content)
	other := f.register("alice")

	reused := f.mustUpload(other, &policy.ID, "alice.png", content)
	if !reused.Dedup || reused.QuotaCharged {
		t.Fatalf("跨用户复用应免费且命中秒传: %+v", reused)
	}
	if reused.Record.ID == owner.Record.ID {
		t.Error("跨用户复用的记录应独立")
	}
	if reused.Record.ObjectKey != owner.Record.ObjectKey {
		t.Error("复用应指向同一对象键")
	}
	if reused.Record.OriginalFilename != "alice.png" {
		t.Errorf("复用记录应保留自身文件名: %q", reused.Record.OriginalFilename)
	}
	alice := f.reloadUser(other.User.ID)
	if alice.UsedImages != 0 || alice.UsedBytes != 0 {
		t.Errorf("复用记录不应占用配额: images=%d bytes=%d", alice.UsedImages, alice.UsedBytes)
	}
	entries, err := os.ReadDir(filepath.Join(f.davRoot, "image"))
	if err != nil {
		t.Fatalf("读取存储目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("复用不应产生新对象, 目录下有 %d 个一级目录", len(entries))
	}
}

func TestDeleteRefundAndObjectDeletion(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	content := pngBytes(t, 5, 5, 5)

	owner := f.mustUpload(f.admin, &policy.ID, "one.png", content)
	other := f.register("bob")
	reused := f.mustUpload(other, &policy.ID, "two.png", content)
	objectPath := filepath.Join(f.davRoot, filepath.FromSlash(owner.Record.ObjectKey))

	first, err := f.svc.Images.Delete(f.admin, owner.Record.ID, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if first.ObjectDeleted || first.Reason != service.DeleteReasonShared {
		t.Errorf("仍有其他引用时不应删除外部对象: %+v", first)
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("外部对象被误删: %v", err)
	}
	if f.reloadUser(f.admin.User.ID).UsedImages != 0 {
		t.Error("删除应回补配额")
	}
	if f.reloadUser(other.User.ID).UsedImages != 1 {
		t.Error("计费应移交给同对象最早的剩余记录")
	}

	second, err := f.svc.Images.Delete(other, reused.Record.ID, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if !second.ObjectDeleted || second.Reason != service.DeleteReasonLastReference {
		t.Errorf("引用归零时应删除外部对象: %+v", second)
	}
	if _, err := os.Stat(objectPath); !os.IsNotExist(err) {
		t.Error("外部对象未被删除")
	}
	if f.reloadUser(other.User.ID).UsedBytes != 0 {
		t.Error("计费移交后的记录被删除, 配额应回补")
	}

	repeated, err := f.svc.Images.Delete(f.admin, owner.Record.ID, "127.0.0.1", "test-agent")
	if err != nil || !repeated.Deleted {
		t.Errorf("重复删除应幂等成功: %+v, %v", repeated, err)
	}
}

func TestDeleteWithoutCapabilityKeepsObject(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	if err := f.svc.Settings.Set("delete_object_on_last_reference", "false"); err != nil {
		t.Fatalf("更新设置失败: %v", err)
	}
	content := pngBytes(t, 7, 4, 4)
	result := f.mustUpload(f.admin, &policy.ID, "keep.png", content)

	response, err := f.svc.Images.Delete(f.admin, result.Record.ID, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if response.ObjectDeleted || response.Reason != service.DeleteReasonSettingOff {
		t.Errorf("开关关闭时应保留外部对象: %+v", response)
	}
	if _, err := os.Stat(filepath.Join(f.davRoot, filepath.FromSlash(result.Record.ObjectKey))); err != nil {
		t.Errorf("外部对象不应被删除: %v", err)
	}
}

func TestConflictRename(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("冲突重命名", "{ext}/{sha256:1}.{ext}", model.ConflictRename)

	first := pngBytes(t, 11, 4, 4)
	firstResult := f.mustUpload(f.admin, &policy.ID, "a.png", first)
	prefix := firstResult.Record.SHA256[:1]

	var second []byte
	for seed := 12; seed < 200; seed++ {
		candidate := pngBytes(t, seed, 4, 4)
		sum := sha256.Sum256(candidate)
		if hex.EncodeToString(sum[:])[:1] == prefix {
			second = candidate
			break
		}
	}
	if second == nil {
		t.Fatal("未构造出哈希前缀相同的测试内容")
	}

	secondResult := f.mustUpload(f.admin, &policy.ID, "b.png", second)
	if secondResult.Record.RelPath == firstResult.Record.RelPath {
		t.Fatal("冲突时应重命名")
	}
	if !strings.Contains(filepath.Base(secondResult.Record.RelPath), "-") {
		t.Errorf("重命名结果应追加随机后缀: %q", secondResult.Record.RelPath)
	}
	if f.recordsFor(policy.ID) != 2 {
		t.Errorf("冲突重命名后应有两条记录, 当前 %d", f.recordsFor(policy.ID))
	}
	if _, err := os.Stat(filepath.Join(f.davRoot, filepath.FromSlash(secondResult.Record.ObjectKey))); err != nil {
		t.Errorf("重命名后的对象未写入: %v", err)
	}
}

func TestConflictFailAndSkip(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("冲突失败", "{ext}/{sha256:1}.{ext}", model.ConflictFail)

	first := pngBytes(t, 21, 4, 4)
	firstResult := f.mustUpload(f.admin, &policy.ID, "a.png", first)
	prefix := firstResult.Record.SHA256[:1]

	var second []byte
	for seed := 22; seed < 300; seed++ {
		candidate := pngBytes(t, seed, 4, 4)
		sum := sha256.Sum256(candidate)
		if hex.EncodeToString(sum[:])[:1] == prefix {
			second = candidate
			break
		}
	}
	if second == nil {
		t.Fatal("未构造出哈希前缀相同的测试内容")
	}

	_, err := f.upload(f.admin, &policy.ID, "b.png", second)
	if err == nil {
		t.Fatal("fail 策略应返回冲突错误")
	}
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "path_conflict" || apiErr.Status != 409 {
		t.Errorf("冲突错误不正确: %v", err)
	}

	skipPolicy := f.createPolicyWithPrefix("冲突跳过", "{ext}/{sha256:1}.{ext}", model.ConflictSkip, "skip")
	skipFirst := f.mustUpload(f.admin, &skipPolicy.ID, "c.png", first)
	_ = skipFirst
	if _, err := f.upload(f.admin, &skipPolicy.ID, "d.png", second); err == nil {
		t.Error("skip 策略在哈希不一致时应返回冲突")
	}
}

func TestConflictOverwrite(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("冲突覆盖", "{ext}/{sha256:1}.{ext}", model.ConflictOverwrite)

	first := pngBytes(t, 31, 4, 4)
	firstResult := f.mustUpload(f.admin, &policy.ID, "a.png", first)
	prefix := firstResult.Record.SHA256[:1]

	var second []byte
	for seed := 32; seed < 400; seed++ {
		candidate := pngBytes(t, seed, 6, 6)
		sum := sha256.Sum256(candidate)
		if hex.EncodeToString(sum[:])[:1] == prefix {
			second = candidate
			break
		}
	}
	if second == nil {
		t.Fatal("未构造出哈希前缀相同的测试内容")
	}

	secondResult := f.mustUpload(f.admin, &policy.ID, "b.png", second)
	if secondResult.Record.ObjectKey != firstResult.Record.ObjectKey {
		t.Errorf("overwrite 策略应复用同一对象键: %q vs %q", secondResult.Record.ObjectKey, firstResult.Record.ObjectKey)
	}
	if secondResult.Record.ID != firstResult.Record.ID {
		t.Errorf("同用户覆盖应复用同一条记录: %d vs %d", secondResult.Record.ID, firstResult.Record.ID)
	}
	if f.recordsFor(policy.ID) != 1 {
		t.Errorf("同用户覆盖不应产生重复记录, 当前 %d 条", f.recordsFor(policy.ID))
	}
	sum := sha256.Sum256(second)
	onDisk, err := os.ReadFile(filepath.Join(f.davRoot, filepath.FromSlash(secondResult.Record.ObjectKey)))
	if err != nil {
		t.Fatalf("读取外部对象失败: %v", err)
	}
	diskSum := sha256.Sum256(onDisk)
	if hex.EncodeToString(diskSum[:]) != hex.EncodeToString(sum[:]) {
		t.Error("外部对象应被覆盖为新内容")
	}

	var updated model.Image
	if err := f.database.First(&updated, firstResult.Record.ID).Error; err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if updated.SHA256 != hex.EncodeToString(sum[:]) {
		t.Error("覆盖时既有记录的哈希应同步更新")
	}
	user := f.reloadUser(f.admin.User.ID)
	if user.UsedImages != 1 {
		t.Errorf("覆盖不应重复计费: used_images=%d", user.UsedImages)
	}
	if user.UsedBytes != int64(len(second)) {
		t.Errorf("覆盖后占用空间应同步为新的体积: used_bytes=%d, 期望 %d", user.UsedBytes, len(second))
	}
}

func TestQuotaEnforcement(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	alice := f.register("alice")

	if _, err := f.svc.Users.Update(alice.User.ID, service.UserInput{
		QuotaMode:  new(model.QuotaModeCount),
		QuotaValue: new(int64(1)),
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("设置配额失败: %v", err)
	}
	refreshed, err := f.svc.Auth.PrincipalFor(alice.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}

	f.mustUpload(refreshed, &policy.ID, "first.png", pngBytes(t, 41, 4, 4))
	_, err = f.upload(refreshed, &policy.ID, "second.png", pngBytes(t, 42, 4, 4))
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "quota_exceeded" {
		t.Fatalf("count 配额超限应返回 quota_exceeded, 实际 %v", err)
	}

	second := f.register("carol")
	if _, err := f.svc.Users.Update(second.User.ID, service.UserInput{
		QuotaMode:  new(model.QuotaModeBytes),
		QuotaValue: new(int64(10)),
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("设置配额失败: %v", err)
	}
	refreshedSecond, err := f.svc.Auth.PrincipalFor(second.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}
	_, err = f.upload(refreshedSecond, &policy.ID, "third.png", pngBytes(t, 43, 8, 8))
	if !errors.As(err, &apiErr) || apiErr.Code != "quota_exceeded" {
		t.Fatalf("bytes 配额超限应返回 quota_exceeded, 实际 %v", err)
	}
}

func TestFormatAndSizeValidation(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)

	if _, err := f.upload(f.admin, &policy.ID, "vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)); err == nil {
		t.Error("SVG 应被系统级拒绝")
	} else {
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "unsupported_format" {
			t.Errorf("SVG 应返回 unsupported_format, 实际 %v", err)
		}
	}
	if _, err := f.upload(f.admin, &policy.ID, "note.txt", []byte("plain text content")); err == nil {
		t.Error("非图片应被拒绝")
	}

	if _, err := f.svc.Users.Update(f.admin.User.ID, service.UserInput{
		QuotaMode:  new(model.QuotaModeBytes),
		QuotaValue: new(int64(1024 * 1024 * 1024)),
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("设置配额失败: %v", err)
	}
	if err := f.svc.Settings.Update(map[string]string{"max_file_size_bytes": "2048"}); err != nil {
		t.Fatalf("更新设置失败: %v", err)
	}
	refreshed, err := f.svc.Auth.PrincipalFor(f.admin.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}
	bigContent := pngBytes(t, 51, 512, 512)
	if len(bigContent) <= 2048 {
		t.Fatalf("测试内容过小, 无法验证体积上限: %d 字节", len(bigContent))
	}
	_, err = f.upload(refreshed, &policy.ID, "big.png", bigContent)
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "file_too_large" {
		t.Fatalf("超过单文件上限应返回 file_too_large, 实际 %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	alice := f.register("alice")

	group, err := f.svc.Groups.Get(f.normalGID)
	if err != nil {
		t.Fatalf("读取用户组失败: %v", err)
	}
	if _, err := f.svc.Groups.Update(f.normalGID, service.GroupInput{
		Name:            group.Name,
		Description:     group.Description,
		Permissions:     group.Permissions.Names(),
		QuotaMode:       group.QuotaMode,
		QuotaValue:      group.QuotaValue,
		AllowedFormats:  group.AllowedFormats,
		UploadRateLimit: new(1),
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("更新用户组失败: %v", err)
	}
	refreshed, err := f.svc.Auth.PrincipalFor(alice.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}

	f.mustUpload(refreshed, &policy.ID, "one.png", pngBytes(t, 61, 4, 4))
	_, err = f.upload(refreshed, &policy.ID, "two.png", pngBytes(t, 62, 4, 4))
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "rate_limited" || apiErr.Status != 429 {
		t.Fatalf("超过速率限制应返回 rate_limited, 实际 %v", err)
	}
	if apiErr.Details == nil {
		t.Error("限流错误应带 retry_after_seconds 详情")
	}
}

func TestPolicyWhitelist(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	alice := f.register("alice")

	_, err := f.upload(alice, &policy.ID, "a.png", pngBytes(t, 71, 4, 4))
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "policy_not_allowed" {
		t.Fatalf("未授权策略应返回 policy_not_allowed, 实际 %v", err)
	}

	f.authorize(policy.ID)
	refreshed, err := f.svc.Auth.PrincipalFor(alice.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}
	allowed, err := f.svc.Policies.AllowedFor(refreshed)
	if err != nil {
		t.Fatalf("读取可用策略失败: %v", err)
	}
	if len(allowed) != 1 || allowed[0].ID != policy.ID {
		t.Fatalf("授权后可用策略不正确: %+v", allowed)
	}
	if _, err := f.upload(refreshed, &policy.ID, "a.png", pngBytes(t, 72, 4, 4)); err != nil {
		t.Fatalf("授权后上传应成功: %v", err)
	}
}

func TestPolicyCreationQueuesInitialScan(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5:2}/{md5}.{ext}", model.ConflictRename)

	var count int64
	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ? AND status = ?", model.TaskPolicyScan, model.TaskPending).
		Count(&count).Error; err != nil {
		t.Fatalf("统计任务失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("创建策略后应入队一次扫描任务, 实际 %d", count)
	}
	_ = policy
}

func TestScanBackfill(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5:2}/{md5}.{ext}", model.ConflictRename)

	imageContent := pngBytes(t, 81, 5, 5)
	imagePath := filepath.Join(f.davRoot, "image", "0a", "existing.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if err := os.WriteFile(imagePath, imageContent, 0o600); err != nil {
		t.Fatalf("准备图片失败: %v", err)
	}
	textPath := filepath.Join(f.davRoot, "image", "0a", "notes.txt")
	if err := os.WriteFile(textPath, []byte("plain notes"), 0o600); err != nil {
		t.Fatalf("准备文本失败: %v", err)
	}

	result, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, nil, false)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if result.Images != 1 || result.Files != 1 {
		t.Fatalf("扫描统计不正确: %+v", result)
	}

	var records []model.Image
	if err := f.database.Where("policy_id = ?", policy.ID).Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("扫描应补录两条记录, 实际 %d", len(records))
	}
	foundImage, foundFile := false, false
	for _, record := range records {
		if record.QuotaCharged || record.Source != model.SourceScan {
			t.Errorf("扫描记录不应计费且来源为 scan: %+v", record)
		}
		if record.ObjectKey == "image/0a/existing.png" {
			foundImage = true
			sum := sha256.Sum256(imageContent)
			if record.SHA256 != hex.EncodeToString(sum[:]) {
				t.Error("扫描记录的 sha256 与内容不一致")
			}
			if record.URL != "https://cdn.example.com/image/0a/existing.png" {
				t.Errorf("扫描记录的 URL 拼接不正确: %q", record.URL)
			}
		}
		if record.ObjectKey == "image/0a/notes.txt" {
			foundFile = true
			if record.Kind != model.KindFile {
				t.Errorf("非图片补录记录 kind 应为 file: %q", record.Kind)
			}
		}
	}
	if !foundImage || !foundFile {
		t.Fatalf("补录记录不完整: image=%v file=%v", foundImage, foundFile)
	}

	list, err := f.svc.Images.List(f.admin, service.ListFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("图库不应显示非图片记录, 当前 %d 条", list.Total)
	}

	second, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, nil, false)
	if err != nil {
		t.Fatalf("重复扫描失败: %v", err)
	}
	if second.Images != 0 || second.Files != 0 || second.Skipped != 2 {
		t.Errorf("重复扫描应幂等跳过已有路径: %+v", second)
	}
}

func TestPathPreviewMatchesUpload(t *testing.T) {
	f := newFixture(t)
	preview, err := f.svc.Policies.Preview(service.PreviewInput{
		PathTemplate: "{md5:2}/{md5}.{ext}",
		ObjectPrefix: "image",
	})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if preview.RelPath != "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png" {
		t.Errorf("预览 rel_path 不正确: %q", preview.RelPath)
	}
	if preview.ObjectKey != "image/0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png" {
		t.Errorf("预览 object_key 不正确: %q", preview.ObjectKey)
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           "非法模板",
		Type:           "webdav",
		PathTemplate:   "fixed/{ext}",
		ConflictPolicy: model.ConflictRename,
		Config:         map[string]string{"endpoint": f.davEndpoint()},
		URLs:           []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com/", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil {
		t.Error("缺少内容哈希变量的模板应被拒绝")
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           "缺少默认域名",
		Type:           "webdav",
		PathTemplate:   "{md5}.{ext}",
		ConflictPolicy: model.ConflictRename,
		Config:         map[string]string{"endpoint": f.davEndpoint()},
		URLs:           []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com/"}},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil {
		t.Error("没有默认分发域名的策略应被拒绝")
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           "覆盖能力不足",
		Type:           "unknown-driver",
		PathTemplate:   "{md5}.{ext}",
		ConflictPolicy: model.ConflictRename,
		URLs:           []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com/", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent"); err == nil {
		t.Error("未知后端类型应被拒绝")
	}
}

func TestPolicyConnectivityTest(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)

	elapsed, err := f.svc.Policies.Test(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("连接测试失败: %v", err)
	}
	if elapsed <= 0 {
		t.Error("连接测试应返回耗时")
	}

	if _, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           "错误地址",
		Type:           "webdav",
		PathTemplate:   "{md5}.{ext}",
		ConflictPolicy: model.ConflictRename,
		Config:         map[string]string{"endpoint": "http://127.0.0.1:1/dav"},
		URLs:           []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com/", IsDefault: true}},
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	var failed model.StoragePolicy
	if err := f.database.Where("name = ?", "错误地址").First(&failed).Error; err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.svc.Policies.Test(ctx, failed.ID); err == nil {
		t.Error("不可达的后端应返回错误")
	}
}

func TestCapabilitiesAndDriverRegistry(t *testing.T) {
	spec, ok := storage.SpecOf("webdav")
	if !ok {
		t.Fatal("未注册 webdav 驱动")
	}
	if !spec.Has(storage.CapDelete) || !spec.Has(storage.CapList) || !spec.Has(storage.CapRead) {
		t.Errorf("webdav 驱动能力声明不完整: %+v", spec.Capabilities)
	}
	if spec.Has(storage.CapPresign) {
		t.Error("webdav 不应声明 presign")
	}
	for _, name := range []string{"endpoint"} {
		found := false
		for _, field := range spec.ConfigFields {
			if field.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("缺少配置字段定义: %s", name)
		}
	}
	if _, err := storage.New("local", storage.Options{}); err == nil {
		t.Error("local 类型不应存在 (违反不做本地存储的约束)")
	}
}

func TestTaskQueueScanAndPurge(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)

	imagePath := filepath.Join(f.davRoot, "image", "queued.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if err := os.WriteFile(imagePath, pngBytes(t, 91, 4, 4), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}

	task, err := f.svc.Tasks.Enqueue(model.TaskPolicyScan, map[string]any{"policy_id": policy.ID, "user_id": f.admin.User.ID})
	if err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if err := f.svc.Tasks.RunNow(task.ID, f.admin); err != nil {
		t.Fatalf("执行任务失败: %v", err)
	}
	var stored model.MaintenanceTask
	if err := f.database.First(&stored, task.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != model.TaskDone {
		t.Errorf("任务状态应为 done: %+v", stored)
	}

	var scanned int64
	if err := f.database.Model(&model.Image{}).Where("policy_id = ? AND object_key = ?", policy.ID, "image/queued.png").Count(&scanned).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("任务执行后应补录该文件, 实际 %d", scanned)
	}

	if _, err := f.svc.Tasks.Enqueue(model.TaskCDNPurge, nil); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	var cdnTask model.MaintenanceTask
	if err := f.database.Where("type = ?", model.TaskCDNPurge).First(&cdnTask).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if err := f.svc.Tasks.RunNow(cdnTask.ID, f.admin); err == nil {
		t.Error("CDN 刷新未接入时应明确失败")
	}
}

func TestUsedBytesReconcile(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	content := pngBytes(t, 101, 4, 4)
	f.mustUpload(f.admin, &policy.ID, "a.png", content)

	if err := f.database.Model(&model.User{}).Where("id = ?", f.admin.User.ID).Updates(map[string]any{
		"used_images": 99,
		"used_bytes":  999999,
	}).Error; err != nil {
		t.Fatalf("篡改计数失败: %v", err)
	}
	fixed, err := f.svc.Images.ReconcileUsed()
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if fixed != 1 {
		t.Errorf("应修正 1 个用户, 实际 %d", fixed)
	}
	user := f.reloadUser(f.admin.User.ID)
	if user.UsedImages != 1 || user.UsedBytes != int64(len(content)) {
		t.Errorf("对账结果不正确: images=%d bytes=%d", user.UsedImages, user.UsedBytes)
	}
}

func TestAuthRegisterLoginAndTotp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: "admin",
		Email:    "other@example.com",
		Password: "password123",
	}, "127.0.0.1", "test"); err == nil {
		t.Error("用户名重复应被拒绝")
	}
	if _, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: "newbie",
		Email:    "admin@example.com",
		Password: "password123",
	}, "127.0.0.1", "test"); err == nil {
		t.Error("邮箱重复应被拒绝")
	}
	if _, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: "weak",
		Email:    "weak@example.com",
		Password: "123",
	}, "127.0.0.1", "test"); err == nil {
		t.Error("弱密码应被拒绝")
	}

	user, err := f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin", Password: "password123"}, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("登录返回用户不正确: %+v", user)
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin@example.com", Password: "wrong"}, "127.0.0.1", "test"); err == nil {
		t.Error("错误密码应被拒绝")
	}

	token, err := f.svc.Auth.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("签发会话失败: %v", err)
	}
	if parsedID, err := f.svc.Auth.ParseSession(token); err != nil || parsedID != user.ID {
		t.Fatalf("会话解析失败: %d, %v", parsedID, err)
	}
	if _, err := f.svc.Auth.ParseSession(token + "tampered"); err == nil {
		t.Error("被篡改的会话令牌应校验失败")
	}

	secret, otpauthURL, err := f.svc.Auth.TOTPSetup(user.ID)
	if err != nil {
		t.Fatalf("生成 TOTP 失败: %v", err)
	}
	if secret == "" || !strings.HasPrefix(otpauthURL, "otpauth://") {
		t.Fatalf("TOTP 生成结果不正确: %q %q", secret, otpauthURL)
	}
	if err := f.svc.Auth.TOTPEnable(user.ID, secret, "000000", "127.0.0.1", "test"); err == nil {
		t.Error("错误的 TOTP 验证码应被拒绝")
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	f := newFixture(t)
	scopes := model.PermUpload
	key, secret, err := f.svc.Keys.Create(f.admin.User.ID, "CI", &scopes, nil)
	if err != nil {
		t.Fatalf("创建密钥失败: %v", err)
	}
	if !strings.HasPrefix(secret, "pk_") || !strings.Contains(secret, ".sk_") {
		t.Fatalf("密钥格式不正确: %q", secret)
	}

	authenticated, err := f.svc.Keys.Authenticate(secret)
	if err != nil {
		t.Fatalf("密钥认证失败: %v", err)
	}
	if authenticated.ID != key.ID {
		t.Error("认证返回的密钥不正确")
	}
	if _, err := f.svc.Keys.Authenticate("pk_deadbeef.sk_invalid"); err == nil {
		t.Error("无效密钥应认证失败")
	}

	principal, err := f.svc.Auth.PrincipalFor(key.UserID, &key.ID, key.Scopes)
	if err != nil {
		t.Fatalf("加载主体失败: %v", err)
	}
	if principal.Has(model.PermManageUsers) {
		t.Error("密钥 scopes 应限制权限范围")
	}

	if err := f.svc.Keys.Revoke(f.admin.User.ID, key.ID); err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	if _, err := f.svc.Keys.Authenticate(secret); err == nil {
		t.Error("已吊销密钥应认证失败")
	}
}

func TestSettingsSecretsEncrypted(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{"captcha_secret": "super-secret-value", "site_name": "Picsky 测试"}); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	var row model.Setting
	if err := f.database.Where("key = ?", "captcha_secret").First(&row).Error; err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	if strings.Contains(row.Value, "super-secret-value") {
		t.Error("秘密设置必须以密文入库")
	}
	plain, err := f.svc.Settings.Secret("captcha_secret")
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if plain != "super-secret-value" {
		t.Errorf("解密结果不正确: %q", plain)
	}
	if f.svc.Settings.VisibleValues()["captcha_secret"] != "" {
		t.Error("下发设置时不应包含秘密值")
	}

	if err := f.svc.Settings.Update(map[string]string{"unknown_key": "x"}); err == nil {
		t.Error("未知设置项应被拒绝")
	}

	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	if err := f.database.Model(&model.StoragePolicy{}).Where("id = ?", policy.ID).
		Update("secrets", "not-encrypted").Error; err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	reloaded, err := f.svc.Policies.Get(policy.ID)
	if err != nil {
		t.Fatalf("重新读取策略失败: %v", err)
	}
	if _, err := f.svc.Policies.Adapter(reloaded); err == nil {
		t.Error("非法凭据密文应报错")
	}
	_ = fmt.Sprintf("%v", policy)
}

func TestUploadRejectsTruncatedContent(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	content := pngBytes(t, 201, 8, 8)

	spooled, err := service.SpoolReader(f.cfg.TempDir, f.cfg.UploadMemoryLimit, bytes.NewReader(content), "big.png", int64(len(content)-1))
	if err != nil {
		t.Fatalf("缓冲失败: %v", err)
	}
	defer spooled.Spool.Close()
	if !spooled.Truncated {
		t.Fatal("超过读取上限时应标记截断")
	}

	_, err = f.svc.Upload.Upload(context.Background(), service.UploadRequest{
		Principal: f.admin,
		PolicyID:  &policy.ID,
		Spooled:   spooled,
		Source:    model.SourceWeb,
	})
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "file_too_large" {
		t.Fatalf("被截断的内容必须拒绝而不是静默入库, 实际 %v", err)
	}
	if f.recordsFor(policy.ID) != 0 {
		t.Error("被拒绝的上传不应产生记录")
	}
}

func TestPolicyRejectsUnsafeObjectPrefix(t *testing.T) {
	f := newFixture(t)
	for _, prefix := range []string{"../shared", "/absolute", "a/../b", "a/./b", "a//b", "a/"} {
		_, err := f.svc.Policies.Create(service.PolicyInput{
			Name:           "非法前缀",
			Type:           "webdav",
			ObjectPrefix:   prefix,
			PathTemplate:   "{md5}.{ext}",
			ConflictPolicy: model.ConflictRename,
			Config:         map[string]string{"endpoint": f.davEndpoint()},
			URLs:           []service.URLInput{{Alias: "CDN", URL: "https://cdn.example.com/", IsDefault: true}},
		}, f.admin, "127.0.0.1", "test-agent")
		if err == nil {
			t.Errorf("对象前缀 %q 应被拒绝", prefix)
		}
	}

	if _, err := f.svc.Policies.Scan(context.Background(), 1, f.admin.User.ID, new("../etc"), false); err == nil {
		t.Error("扫描子路径包含 .. 时应被拒绝")
	}
}

func TestConcurrentQuotaEnforcement(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("并发配额", "{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	alice := f.register("alice")

	if _, err := f.svc.Users.Update(alice.User.ID, service.UserInput{
		QuotaMode:  new(model.QuotaModeCount),
		QuotaValue: new(int64(3)),
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("设置配额失败: %v", err)
	}
	principal, err := f.svc.Auth.PrincipalFor(alice.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}

	const workers = 8
	contents := make([][]byte, workers)
	for index := range workers {
		contents[index] = pngBytes(t, 300+index, 8, 8)
	}

	errorsByWorker := make([]error, workers)
	done := make(chan int, workers)
	for index := range workers {
		go func(slot int) {
			_, uploadErr := f.upload(principal, &policy.ID, fmt.Sprintf("c%d.png", slot), contents[slot])
			errorsByWorker[slot] = uploadErr
			done <- slot
		}(index)
	}
	for range workers {
		<-done
	}

	success, rejected := 0, 0
	for _, uploadErr := range errorsByWorker {
		if uploadErr == nil {
			success++
			continue
		}
		var apiErr *apperr.Error
		if !errors.As(uploadErr, &apiErr) || apiErr.Code != "quota_exceeded" {
			t.Fatalf("并发上传返回了非预期错误: %v", uploadErr)
		}
		rejected++
	}

	user := f.reloadUser(alice.User.ID)
	if success != 3 || rejected != workers-3 {
		t.Fatalf("配额必须被原子执行: 成功 %d 拒绝 %d", success, rejected)
	}
	if user.UsedImages != 3 {
		t.Fatalf("配额计数超出上限: used_images=%d", user.UsedImages)
	}
}

func TestConcurrentSameContentChargedOnce(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("并发秒传", "{md5}.{ext}", model.ConflictRename)
	content := pngBytes(t, 401, 8, 8)

	const workers = 6
	done := make(chan int, workers)
	for index := range workers {
		go func(slot int) {
			_, _ = f.upload(f.admin, &policy.ID, fmt.Sprintf("same%d.png", slot), content)
			done <- slot
		}(index)
	}
	for range workers {
		<-done
	}

	var charged int64
	if err := f.database.Model(&model.Image{}).
		Where("policy_id = ? AND object_key = ? AND deleted_at IS NULL AND quota_charged = ?", policy.ID, "image/"+md5RelPath(content), true).
		Count(&charged).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if charged != 1 {
		t.Fatalf("同一对象键只应有一条计费记录, 实际 %d", charged)
	}
	user := f.reloadUser(f.admin.User.ID)
	if user.UsedImages != 1 {
		t.Fatalf("并发上传相同内容应只计费一次: used_images=%d", user.UsedImages)
	}
}

// md5RelPath 按默认模板推导相对路径, 用于断言对象键.
func md5RelPath(content []byte) string {
	sum := md5.Sum(content)
	hexed := hex.EncodeToString(sum[:])
	return hexed + ".png"
}

func TestLoginFailureThrottle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var lastErr error
	for attempt := range service.LoginFailureLimit {
		_, lastErr = f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin", Password: "wrong-password"}, "203.0.113.10", "test")
		if lastErr == nil {
			t.Fatalf("第 %d 次错误密码应失败", attempt+1)
		}
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin", Password: "wrong-password"}, "203.0.113.10", "test"); err == nil {
		t.Fatal("超过失败上限后应拒绝继续尝试")
	} else {
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "rate_limited" || apiErr.Status != 429 {
			t.Fatalf("超限应返回 rate_limited, 实际 %v", err)
		}
	}

	// 正确密码同样被临时拒绝, 但其他来源不受影响.
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin", Password: "password123"}, "203.0.113.10", "test"); err == nil {
		t.Error("限流窗口内即使凭据正确也应拒绝")
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Identifier: "admin", Password: "password123"}, "198.51.100.7", "test"); err != nil {
		t.Errorf("其他来源不应被牵连: %v", err)
	}
}

func TestBatchDeleteRequiresPermission(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	uploaded := f.mustUpload(f.admin, &policy.ID, "keep.png", pngBytes(t, 501, 4, 4))

	group, err := f.svc.Groups.Create(service.GroupInput{
		Name:        "无删除权限组",
		Permissions: []string{"upload"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建用户组失败: %v", err)
	}
	viewer, err := f.svc.Users.Create(service.UserInput{
		Username: "viewer",
		Email:    "viewer@example.com",
		Password: "password123",
		GroupID:  &group.ID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	principal, err := f.svc.Auth.PrincipalFor(viewer.ID, nil, nil)
	if err != nil {
		t.Fatalf("加载主体失败: %v", err)
	}

	_, err = f.svc.Images.BatchDelete(principal, []int64{uploaded.Record.ID}, "127.0.0.1", "test-agent")
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "forbidden" {
		t.Fatalf("无删除权限的批量删除应返回 forbidden, 实际 %v", err)
	}
	record := f.reloadUser(f.admin.User.ID)
	if record.UsedImages != 1 {
		t.Errorf("被拒绝的批量删除不应改动记录: used_images=%d", record.UsedImages)
	}
	var stored model.Image
	if err := f.database.First(&stored, uploaded.Record.ID).Error; err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if stored.DeletedAt != nil {
		t.Error("被拒绝的批量删除不应软删记录")
	}
}

func TestDanglingDefaultPolicyFallsBack(t *testing.T) {
	f := newFixture(t)
	mainPolicy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(mainPolicy.ID)
	alice := f.register("alice")

	temporary := f.createPolicyWithPrefix("临时策略", "{md5}.{ext}", model.ConflictRename, "tmp")
	defaultPolicyID := temporary.ID
	if _, err := f.svc.Users.Update(alice.User.ID, service.UserInput{
		DefaultPolicyID: &defaultPolicyID,
	}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("设置默认策略失败: %v", err)
	}
	if err := f.svc.Policies.Delete(temporary.ID, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("删除策略失败: %v", err)
	}

	refreshed, err := f.svc.Auth.PrincipalFor(alice.User.ID, nil, nil)
	if err != nil {
		t.Fatalf("刷新主体失败: %v", err)
	}
	result := f.mustUpload(refreshed, nil, "fallback.png", pngBytes(t, 502, 4, 4))
	if result.Record.PolicyID != mainPolicy.ID {
		t.Fatalf("默认策略失效后应退化为组内第一个可用策略: %d", result.Record.PolicyID)
	}
}
