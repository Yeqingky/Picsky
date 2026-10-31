package service_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
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

func optional[T any](value T) service.OptionalField[T] {
	return service.OptionalField[T]{Set: true, Value: &value}
}

func optionalNull[T any]() service.OptionalField[T] {
	return service.OptionalField[T]{Set: true}
}

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
	f.t.Helper()
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           name,
		Type:           "webdav",
		Enabled:        new(true),
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

// recordsFor 统计某策略下的记录数.
func (f *fixture) recordsFor(policyID int64) int64 {
	f.t.Helper()
	var count int64
	if err := f.database.Model(&model.Image{}).Where("policy_id = ?", policyID).Count(&count).Error; err != nil {
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

// setReuseReleasedSeq 打开或关闭策略的释放序号复用.
func (f *fixture) setReuseReleasedSeq(policyID int64, enabled bool) {
	f.t.Helper()
	value := enabled
	if _, err := f.svc.Policies.Update(policyID, service.PolicyInput{ReuseReleasedSeq: &value}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		f.t.Fatalf("更新序号复用开关失败: %v", err)
	}
}

// sequenceRow 读取策略的序号计数器.
func (f *fixture) sequenceRow(policyID int64) model.PolicySequence {
	f.t.Helper()
	var row model.PolicySequence
	if err := f.database.Where("policy_id = ?", policyID).First(&row).Error; err != nil {
		f.t.Fatalf("读取序号计数器失败: %v", err)
	}
	return row
}

// mustDelete 删除记录并要求成功.
func (f *fixture) mustDelete(principal *service.Principal, id int64) *service.DeleteResult {
	f.t.Helper()
	result, err := f.svc.Images.Delete(principal, id, "127.0.0.1", "test-agent")
	if err != nil {
		f.t.Fatalf("删除记录失败: %v", err)
	}
	return result
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
	if record.ObjectKey != record.RelPath {
		t.Errorf("对象键应等于相对路径: %q", record.ObjectKey)
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
	entries, err := os.ReadDir(f.davRoot)
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

	_, repeatErr := f.svc.Images.Delete(f.admin, owner.Record.ID, "127.0.0.1", "test-agent")
	var repeatAPIErr *apperr.Error
	if !errors.As(repeatErr, &repeatAPIErr) || repeatAPIErr.Code != "not_found" {
		t.Errorf("物理删除后重复删除应返回 not_found, 实际 %v", repeatErr)
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
	if !strings.Contains(filepath.Base(secondResult.Record.RelPath), "-1.") {
		t.Errorf("重命名结果应追加递增数字后缀: %q", secondResult.Record.RelPath)
	}
	if f.recordsFor(policy.ID) != 2 {
		t.Errorf("冲突重命名后应有两条记录, 当前 %d", f.recordsFor(policy.ID))
	}
	if _, err := os.Stat(filepath.Join(f.davRoot, filepath.FromSlash(secondResult.Record.ObjectKey))); err != nil {
		t.Errorf("重命名后的对象未写入: %v", err)
	}
}

func TestConflictSkip(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("冲突跳过", "{ext}/{sha256:1}.{ext}", model.ConflictSkip)

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
		t.Fatal("skip 策略在哈希不一致时应返回冲突")
	}
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "path_conflict" || apiErr.Status != 409 {
		t.Errorf("冲突错误不正确: %v", err)
	}
}

func TestUnsupportedConflictPoliciesAreRejected(t *testing.T) {
	f := newFixture(t)
	for _, conflict := range []string{"overwrite", "fail"} {
		input := service.PolicyInput{
			Name:           "unsupported-" + conflict,
			Type:           "webdav",
			PathTemplate:   "{seq}.{ext}",
			ConflictPolicy: conflict,
			Config:         map[string]string{"endpoint": f.davEndpoint()},
			URLs:           []service.URLInput{{Alias: "cdn", URL: "https://cdn.example.com/image/", IsDefault: true}},
		}
		err := f.svc.Policies.Validate(&input, nil)
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
			t.Errorf("策略不应接受 %q, 错误: %v", conflict, err)
		}
		if err := f.svc.Settings.Update(map[string]string{"default_conflict_policy": conflict}); !errors.As(err, &apiErr) ||
			apiErr.Code != "invalid_request" {
			t.Errorf("系统设置不应接受默认策略 %q, 错误: %v", conflict, err)
		}
	}
}

func TestPolicyReorder(t *testing.T) {
	f := newFixture(t)
	first := f.createPolicy("排序一", "{md5}.{ext}", model.ConflictRename)
	second := f.createPolicy("排序二", "{sha256}.{ext}", model.ConflictRename)
	third := f.createPolicy("排序三", "{sha512}.{ext}", model.ConflictRename)

	if first.Sort != 1 || second.Sort != 2 || third.Sort != 3 {
		t.Fatalf("新建策略应依次追加排序: %d %d %d", first.Sort, second.Sort, third.Sort)
	}

	if err := f.svc.Policies.Reorder(
		[]int64{third.ID, first.ID, second.ID},
		f.admin, "127.0.0.1", "test-agent",
	); err != nil {
		t.Fatalf("重排策略失败: %v", err)
	}
	policies, err := f.svc.Policies.List()
	if err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	want := []int64{third.ID, first.ID, second.ID}
	if len(policies) != len(want) {
		t.Fatalf("策略数量 = %d, 期望 %d", len(policies), len(want))
	}
	for index, policy := range policies {
		if policy.ID != want[index] {
			t.Errorf("第 %d 项策略 = %d, 期望 %d", index, policy.ID, want[index])
		}
		if policy.Sort != index+1 {
			t.Errorf("第 %d 项排序 = %d, 期望 %d", index, policy.Sort, index+1)
		}
	}

	for name, ids := range map[string][]int64{
		"空顺序":    {},
		"重复项":    {first.ID, first.ID},
		"无效 ID":  {0},
		"不存在的策略": {first.ID, 999999},
	} {
		err := f.svc.Policies.Reorder(ids, f.admin, "127.0.0.1", "test-agent")
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
			t.Errorf("%s 应返回 invalid_request, 实际 %v", name, err)
		}
	}
}

func TestPolicyHint(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("提示策略", "{md5}.{ext}", model.ConflictRename)

	hint := "上传前请确认图片符合规范"
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{Hint: &hint}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("更新提示文字失败: %v", err)
	}
	updated, err := f.svc.Policies.Get(policy.ID)
	if err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	if updated.Hint != hint {
		t.Errorf("提示文字 = %q, 期望 %q", updated.Hint, hint)
	}
	if updated.Name != policy.Name {
		t.Errorf("仅更新提示文字时不应改动策略名称: %q", updated.Name)
	}
	if updated.Sort != policy.Sort {
		t.Errorf("仅更新提示文字时不应改动排序: %d", updated.Sort)
	}

	empty := ""
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{Hint: &empty}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("清空提示文字失败: %v", err)
	}
	cleared, err := f.svc.Policies.Get(policy.ID)
	if err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	if cleared.Hint != "" {
		t.Errorf("提示文字应被清空, 实际 %q", cleared.Hint)
	}

	tooLong := strings.Repeat("提", 501)
	_, err = f.svc.Policies.Update(policy.ID, service.PolicyInput{Hint: &tooLong}, f.admin, "127.0.0.1", "test-agent")
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
		t.Errorf("超长提示文字应返回 invalid_request, 实际 %v", err)
	}
}

func TestPolicyImageCount(t *testing.T) {
	f := newFixture(t)
	used := f.createPolicy("有图片", "{md5}.{ext}", model.ConflictRename)
	empty := f.createPolicy("无图片", "{sha256}.{ext}", model.ConflictRename)
	f.mustUpload(f.admin, &used.ID, "used.png", pngBytes(t, 71, 4, 4))

	views, err := f.svc.Policies.List()
	if err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	counts := make(map[int64]int64, len(views))
	for _, view := range views {
		counts[view.ID] = view.ImageCount
	}
	if counts[used.ID] != 1 {
		t.Errorf("有图片策略数量 = %d, 期望 1", counts[used.ID])
	}
	if counts[empty.ID] != 0 {
		t.Errorf("无图片策略数量 = %d, 期望 0", counts[empty.ID])
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

// TestSeedWritesGroupLimits 校验初始化阶段把限额参数写入默认用户组 (不再由系统设置承载).
func TestSeedWritesGroupLimits(t *testing.T) {
	f := newFixture(t)
	groups, err := f.svc.Groups.List()
	if err != nil {
		t.Fatalf("读取用户组失败: %v", err)
	}
	seen := map[string]bool{}
	for _, group := range groups {
		switch group.Name {
		case "管理员", "普通用户":
			seen[group.Name] = true
			if group.UploadRateLimit == nil || *group.UploadRateLimit != 60 {
				t.Errorf("用户组 %s 应写入上传速率 60: %v", group.Name, group.UploadRateLimit)
			}
		}
	}
	for _, name := range []string{"管理员", "普通用户"} {
		if !seen[name] {
			t.Errorf("缺少默认用户组 %s", name)
		}
	}
}

// TestBuiltinGroupGuards 校验内置用户组不可删除, 不可修改权限位, 不可设为新用户默认组.
func TestBuiltinGroupGuards(t *testing.T) {
	f := newFixture(t)
	groups, err := f.svc.Groups.List()
	if err != nil {
		t.Fatalf("读取用户组失败: %v", err)
	}
	var adminGroupID, readonlyID int64
	for _, group := range groups {
		if !group.IsSystem {
			continue
		}
		switch group.Name {
		case "管理员":
			adminGroupID = group.ID
		case "只读":
			readonlyID = group.ID
		}
	}
	if adminGroupID == 0 || readonlyID == 0 {
		t.Fatalf("管理员与只读组应标记 is_system: %+v", groups)
	}

	rejected := func(err error, action string) {
		t.Helper()
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
			t.Errorf("%s 应返回 invalid_request, 实际 %v", action, err)
		}
	}

	readonly, err := f.svc.Groups.Get(readonlyID)
	if err != nil {
		t.Fatalf("读取只读组失败: %v", err)
	}
	original := service.GroupInput{
		Name:        readonly.Name,
		Sort:        optional(readonly.Sort),
		Permissions: readonly.Permissions.Names(),
	}
	if _, err := f.svc.Groups.Update(readonlyID, original, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("内置用户组原样更新应通过: %v", err)
	}

	changed := original
	changed.Permissions = []string{"upload"}
	_, err = f.svc.Groups.Update(readonlyID, changed, f.admin, "127.0.0.1", "test-agent")
	rejected(err, "修改内置用户组权限位")

	asDefault := original
	asDefault.IsDefault = new(true)
	_, err = f.svc.Groups.Update(readonlyID, asDefault, f.admin, "127.0.0.1", "test-agent")
	rejected(err, "把内置用户组设为默认组")

	rejected(f.svc.Groups.Delete(adminGroupID, f.admin, "127.0.0.1", "test-agent"), "删除内置用户组")
	rejected(f.svc.Settings.Set("default_group_id", fmt.Sprintf("%d", readonlyID)), "把内置用户组写为新用户默认组")

	// 绕过校验直接写库: 注册仍不落在内置组.
	if err := f.database.Model(&model.Setting{}).Where("key = ?", "default_group_id").
		Update("value", fmt.Sprintf("%d", readonlyID)).Error; err != nil {
		t.Fatalf("写入默认组设置失败: %v", err)
	}
	if err := f.svc.Settings.Reload(); err != nil {
		t.Fatalf("重载设置失败: %v", err)
	}
	member := f.register("bob")
	joined, err := f.svc.Groups.Get(member.User.GroupID)
	if err != nil {
		t.Fatalf("读取注册账号的用户组失败: %v", err)
	}
	if joined.IsSystem {
		t.Errorf("新用户不应进入内置用户组: %s", joined.Name)
	}
}

func TestPermissionDelegationIsBoundToActor(t *testing.T) {
	f := newFixture(t)
	managerGroup, err := f.svc.Groups.Create(service.GroupInput{
		Name:        "受限管理组",
		Permissions: []string{"upload", "delete_own", "manage_keys", "manage_users", "manage_groups", "manage_settings"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建受限管理组失败: %v", err)
	}
	manager, err := f.svc.Users.Create(service.UserInput{
		Username: "delegator",
		Email:    "delegator@example.com",
		Password: "password123",
		GroupID:  &managerGroup.ID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建受限管理用户失败: %v", err)
	}
	scopes := int64(model.PermUpload | model.PermDeleteOwn | model.PermManageKeys | model.PermManageUsers | model.PermManageGroups)
	actor, err := f.svc.Auth.PrincipalFor(manager.ID, nil, &scopes)
	if err != nil {
		t.Fatalf("加载受限管理主体失败: %v", err)
	}
	fullActor, err := f.svc.Auth.PrincipalFor(manager.ID, nil, nil)
	if err != nil {
		t.Fatalf("加载管理主体失败: %v", err)
	}

	var adminGroup model.Group
	if err := f.database.Where("name = ?", "管理员").First(&adminGroup).Error; err != nil {
		t.Fatalf("读取管理员组失败: %v", err)
	}
	rejectExcess := func(err error, action string) {
		t.Helper()
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
			t.Errorf("%s 应因超出当前权限而返回 invalid_request, 实际: %v", action, err)
		}
	}
	rejectForbidden := func(err error, action string) {
		t.Helper()
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "forbidden" || apiErr.Status != 403 {
			t.Errorf("%s 应因目标权限超出当前权限而返回 403 forbidden, 实际: %v", action, err)
		}
	}

	bannedStatus := model.UserStatusBanned
	quotaMode := model.QuotaModeCount
	quotaValue := int64(1)
	defaultPolicyID := int64(1)
	for _, tc := range []struct {
		action string
		input  service.UserInput
	}{
		{"修改管理员密码", service.UserInput{Password: "attacker-pass-123"}},
		{"封禁管理员", service.UserInput{Status: &bannedStatus}},
		{"覆盖管理员配额", service.UserInput{QuotaMode: &quotaMode, QuotaValue: &quotaValue}},
		{"设置管理员默认策略", service.UserInput{DefaultPolicyID: &defaultPolicyID}},
	} {
		_, err := f.svc.Users.Update(f.admin.User.ID, tc.input, actor, "127.0.0.1", "test-agent")
		rejectForbidden(err, tc.action)
	}
	rejectForbidden(f.svc.Users.Delete(f.admin.User.ID, actor, "127.0.0.1", "test-agent"), "删除管理员")
	_, err = f.svc.Groups.Update(adminGroup.ID, service.GroupInput{Name: "被改名的管理员组"}, actor, "127.0.0.1", "test-agent")
	rejectForbidden(err, "修改管理员组")
	rejectForbidden(f.svc.Groups.Delete(adminGroup.ID, actor, "127.0.0.1", "test-agent"), "删除管理员组")
	rejectForbidden(f.svc.Groups.SetPolicies(adminGroup.ID, nil, actor, "127.0.0.1", "test-agent"), "覆盖管理员组策略")
	reloadedAdminGroup, err := f.svc.Groups.Get(adminGroup.ID)
	if err != nil {
		t.Fatalf("读取管理员组失败: %v", err)
	}
	if reloadedAdminGroup.Name != "管理员" || reloadedAdminGroup.Permissions != model.AllPermissions() {
		t.Errorf("被拒绝的用户组操作不应改动管理员组: %+v", reloadedAdminGroup)
	}
	if _, err := f.svc.Auth.Login(context.Background(), service.LoginInput{
		Email: "admin@example.com", Password: "password123",
	}, "127.0.0.1", "test-agent"); err != nil {
		t.Errorf("被拒绝的密码修改不应影响管理员登录: %v", err)
	}

	secondAdmin, err := f.svc.Users.Create(service.UserInput{
		Username: "secondadmin",
		Email:    "second-admin@example.com",
		Password: "password123",
		GroupID:  &adminGroup.ID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建第二个管理员失败: %v", err)
	}
	if _, err := f.svc.Users.Update(secondAdmin.ID, service.UserInput{Status: &bannedStatus}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("封禁第二个管理员失败: %v", err)
	}
	normalStatus := model.UserStatusNormal
	_, err = f.svc.Users.Update(secondAdmin.ID, service.UserInput{Status: &normalStatus}, actor, "127.0.0.1", "test-agent")
	rejectForbidden(err, "解封管理员")

	_, err = f.svc.Users.Update(manager.ID, service.UserInput{GroupID: &adminGroup.ID}, fullActor, "127.0.0.1", "test-agent")
	rejectExcess(err, "将自己移入管理员组")

	_, err = f.svc.Users.Create(service.UserInput{
		Username: "promoted",
		Email:    "promoted@example.com",
		Password: "password123",
		GroupID:  &adminGroup.ID,
	}, actor, "127.0.0.1", "test-agent")
	rejectExcess(err, "创建管理员用户")

	isDefault := true
	_, err = f.svc.Groups.Create(service.GroupInput{
		Name:        "越权默认组",
		IsDefault:   &isDefault,
		Permissions: model.AllPermissions().Names(),
	}, actor, "127.0.0.1", "test-agent")
	rejectExcess(err, "创建超出权限的默认组")
	var defaultGroup model.Group
	if err := f.database.Where("is_default = ?", true).First(&defaultGroup).Error; err != nil {
		t.Fatalf("读取默认组失败: %v", err)
	}
	if defaultGroup.ID != f.normalGID {
		t.Errorf("越权请求不应替换默认组: got %d, want %d", defaultGroup.ID, f.normalGID)
	}

	ordinaryGroup, err := f.svc.Groups.Get(f.normalGID)
	if err != nil {
		t.Fatalf("读取普通用户组失败: %v", err)
	}
	_, err = f.svc.Groups.Update(ordinaryGroup.ID, service.GroupInput{
		Name:        ordinaryGroup.Name,
		Permissions: model.AllPermissions().Names(),
	}, actor, "127.0.0.1", "test-agent")
	rejectExcess(err, "更新用户组以增加越权权限")

	_, err = f.svc.Groups.Create(service.GroupInput{
		Name:        "超出 API 密钥范围",
		Permissions: []string{"manage_settings"},
	}, actor, "127.0.0.1", "test-agent")
	rejectExcess(err, "委派 API 密钥未授权的权限")

	delegatedGroup, err := f.svc.Groups.Create(service.GroupInput{
		Name:        "可委派管理组",
		Permissions: []string{"manage_groups"},
	}, actor, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("委派自身已有权限应成功: %v", err)
	}
	member, err := f.svc.Users.Create(service.UserInput{
		Username: "member",
		Email:    "member@example.com",
		Password: "password123",
		GroupID:  &ordinaryGroup.ID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建普通用户失败: %v", err)
	}
	if _, err := f.svc.Users.Update(member.ID, service.UserInput{GroupID: &delegatedGroup.ID}, actor, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("分配权限子集内的用户组应成功: %v", err)
	}
}

func TestGroupUpdatePreservesOmittedFields(t *testing.T) {
	f := newFixture(t)
	created, err := f.svc.Groups.Create(service.GroupInput{
		Name:             "保留字段组",
		Description:      optional("原始说明"),
		Permissions:      []string{"upload"},
		QuotaMode:        optional(model.QuotaModeCount),
		QuotaValue:       optional(int64(10)),
		MaxFileSizeBytes: optional(int64(2048)),
		UploadRateLimit:  optional(3),
		Sort:             optional(7),
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建用户组失败: %v", err)
	}

	updated, err := f.svc.Groups.Update(created.ID, service.GroupInput{Name: "改名"}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("仅更新名称失败: %v", err)
	}
	if updated.Description != "原始说明" || updated.Permissions != model.PermUpload ||
		updated.QuotaMode == nil || *updated.QuotaMode != model.QuotaModeCount ||
		updated.QuotaValue == nil || *updated.QuotaValue != 10 ||
		updated.MaxFileSizeBytes == nil || *updated.MaxFileSizeBytes != 2048 ||
		updated.UploadRateLimit == nil || *updated.UploadRateLimit != 3 || updated.Sort != 7 {
		t.Errorf("仅更新名称不应清空其他字段: %+v", updated)
	}

	updated, err = f.svc.Groups.Update(created.ID, service.GroupInput{QuotaMode: optional("")}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("清空配额方式失败: %v", err)
	}
	if updated.QuotaMode != nil || updated.QuotaValue != nil {
		t.Errorf("空配额方式应清空配额: %+v", updated)
	}
	if updated.MaxFileSizeBytes == nil || *updated.MaxFileSizeBytes != 2048 ||
		updated.UploadRateLimit == nil || *updated.UploadRateLimit != 3 {
		t.Errorf("清空配额不应清空上传限额: %+v", updated)
	}

	updated, err = f.svc.Groups.Update(created.ID, service.GroupInput{
		QuotaMode:  optional(model.QuotaModeBytes),
		QuotaValue: optional(int64(5)),
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("重新设置配额失败: %v", err)
	}
	updated, err = f.svc.Groups.Update(created.ID, service.GroupInput{
		QuotaMode:  optionalNull[string](),
		QuotaValue: optionalNull[int64](),
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("显式 null 清空配额失败: %v", err)
	}
	if updated.QuotaMode != nil || updated.QuotaValue != nil {
		t.Errorf("显式 null 应清空配额: %+v", updated)
	}

	updated, err = f.svc.Groups.Update(created.ID, service.GroupInput{Permissions: []string{"delete_own"}}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("移除上传权限失败: %v", err)
	}
	if updated.Permissions != model.PermDeleteOwn || updated.QuotaMode != nil || updated.QuotaValue != nil ||
		updated.MaxFileSizeBytes != nil || updated.UploadRateLimit != nil {
		t.Errorf("移除上传权限应清空配额与上传限额: %+v", updated)
	}
	if updated.Description != "原始说明" || updated.Sort != 7 {
		t.Errorf("移除上传权限不应清空说明或排序: %+v", updated)
	}

	updated, err = f.svc.Groups.Update(created.ID, service.GroupInput{Description: optional(""), Sort: optional(0)}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("清空说明与排序失败: %v", err)
	}
	if updated.Description != "" || updated.Sort != 0 {
		t.Errorf("显式空值应清空说明与排序: %+v", updated)
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
		Description:     optional(group.Description),
		Permissions:     group.Permissions.Names(),
		UploadRateLimit: optional(1),
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
	imagePath := filepath.Join(f.davRoot, "0a", "existing.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if err := os.WriteFile(imagePath, imageContent, 0o600); err != nil {
		t.Fatalf("准备图片失败: %v", err)
	}
	textPath := filepath.Join(f.davRoot, "0a", "notes.txt")
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
		if record.ObjectKey == "0a/existing.png" {
			foundImage = true
			sum := sha256.Sum256(imageContent)
			if record.SHA256 != hex.EncodeToString(sum[:]) {
				t.Error("扫描记录的 sha256 与内容不一致")
			}
			if record.URL != "https://cdn.example.com/image/0a/existing.png" {
				t.Errorf("扫描记录的 URL 拼接不正确: %q", record.URL)
			}
		}
		if record.ObjectKey == "0a/notes.txt" {
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

func TestScannedSequenceContinuesAfterHighestImportedPath(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("递增路径", "{seq}.{ext}", model.ConflictRename)

	for index, number := range []int{1, 929} {
		content := pngBytes(t, 90+index, 4, 4)
		objectPath := filepath.Join(f.davRoot, fmt.Sprintf("%d.png", number))
		if err := os.WriteFile(objectPath, content, 0o600); err != nil {
			t.Fatalf("准备扫描对象失败: %v", err)
		}
	}
	scan, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, nil, false)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if scan.Images != 2 {
		t.Fatalf("扫描应补录两个图片对象, 实际 %+v", scan)
	}

	result := f.mustUpload(f.admin, &policy.ID, "new.png", pngBytes(t, 95, 4, 4))
	if result.Record.RelPath != "930.png" {
		t.Fatalf("扫描到 929.png 后应生成 930.png, 实际 %q", result.Record.RelPath)
	}
}

func TestSequenceIsSharedAcrossFormats(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("跨格式序号", "{seq}.{ext}", model.ConflictRename)

	first := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 900, 4, 4))
	if first.Record.RelPath != "1.png" {
		t.Fatalf("首个文件应为 1.png, 实际 %q", first.Record.RelPath)
	}

	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("编码 JPEG 失败: %v", err)
	}
	second := f.mustUpload(f.admin, &policy.ID, "b.jpg", encoded.Bytes())
	if second.Record.RelPath != "2.jpg" {
		t.Fatalf("跨格式应继续使用共享序号, 实际 %q", second.Record.RelPath)
	}
}

func TestScanReconcilesSequenceCounter(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("扫描对账序号", "{seq}.{ext}", model.ConflictRename)

	first := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 901, 4, 4))
	if first.Record.RelPath != "1.png" {
		t.Fatalf("首个文件应为 1.png, 实际 %q", first.Record.RelPath)
	}
	for name, seed := range map[string]int{"50.webp": 902, "60.jpg": 903} {
		if err := os.WriteFile(filepath.Join(f.davRoot, name), pngBytes(t, seed, 4, 4), 0o600); err != nil {
			t.Fatalf("准备扫描对象失败: %v", err)
		}
	}
	if _, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, nil, false); err != nil {
		t.Fatalf("扫描失败: %v", err)
	}

	next := f.mustUpload(f.admin, &policy.ID, "b.png", pngBytes(t, 904, 4, 4))
	if next.Record.RelPath != "61.png" {
		t.Fatalf("扫描后应从 61 继续, 实际 %q", next.Record.RelPath)
	}
}

func TestSequenceTemplateRequiresRename(t *testing.T) {
	f := newFixture(t)
	input := service.PolicyInput{
		Name:           "非法序号组合",
		Type:           "webdav",
		PathTemplate:   "{seq}.{ext}",
		ConflictPolicy: model.ConflictSkip,
		Config:         map[string]string{"endpoint": f.davEndpoint()},
		URLs:           []service.URLInput{{Alias: "cdn", URL: "https://cdn.example.com/image/", IsDefault: true}},
	}
	err := f.svc.Policies.Validate(&input, nil)
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
		t.Fatalf("{seq} 与 skip 组合应被拒绝, 错误: %v", err)
	}
}

func TestConcurrentSequenceUploadsUseDistinctPaths(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("并发递增路径", "{seq}.{ext}", model.ConflictRename)
	contents := [][]byte{pngBytes(t, 96, 4, 4), pngBytes(t, 97, 4, 4)}
	type outcome struct {
		record *model.Image
		err    error
	}
	results := make(chan outcome, len(contents))
	var wait sync.WaitGroup
	for _, content := range contents {
		content := content
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := f.upload(f.admin, &policy.ID, "parallel.png", content)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			results <- outcome{record: result.Record}
		}()
	}
	wait.Wait()
	close(results)

	paths := make(map[string]bool, len(contents))
	for result := range results {
		if result.err != nil {
			t.Errorf("并发上传失败: %v", result.err)
			continue
		}
		paths[result.record.RelPath] = true
		if _, err := os.Stat(filepath.Join(f.davRoot, filepath.FromSlash(result.record.ObjectKey))); err != nil {
			t.Errorf("递增对象未写入: %v", err)
		}
	}
	if !paths["1.png"] || !paths["2.png"] {
		t.Errorf("并发上传应占用不同递增路径, 实际 %v", paths)
	}
}

func TestDeletedSequenceRecordedWhenReuseDisabled(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("序号释放", "{seq}.{ext}", model.ConflictRename)

	first := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 120, 4, 4))
	second := f.mustUpload(f.admin, &policy.ID, "b.png", pngBytes(t, 121, 4, 4))
	if first.Record.RelPath != "1.png" || second.Record.RelPath != "2.png" {
		t.Fatalf("初始序号不正确: %q %q", first.Record.RelPath, second.Record.RelPath)
	}

	deleted := f.mustDelete(f.admin, first.Record.ID)
	if !deleted.ObjectDeleted {
		t.Fatalf("删除应同时移除外部对象: %+v", deleted)
	}
	row := f.sequenceRow(policy.ID)
	if row.Allocated != 2 || row.Released != "[1]" {
		t.Fatalf("删除后计数器应为 allocated=2 released=[1], 实际 %+v", row)
	}

	var done int64
	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ? AND status = ?", model.TaskObjectDelete, model.TaskDone).
		Count(&done).Error; err != nil {
		t.Fatalf("统计维护任务失败: %v", err)
	}
	if done != 1 {
		t.Errorf("成功删除也应写入维护队列, 实际 done=%d", done)
	}

	third := f.mustUpload(f.admin, &policy.ID, "c.png", pngBytes(t, 122, 4, 4))
	if third.Record.RelPath != "3.png" {
		t.Fatalf("默认关闭复用时不应使用释放序号, 实际 %q", third.Record.RelPath)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[1]" {
		t.Errorf("关闭复用时仍应保留释放队列, 实际 %q", row.Released)
	}
}

func TestReleasedSequencesReusedInFIFOOrder(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("序号复用", "{seq}.{ext}", model.ConflictRename)
	f.setReuseReleasedSeq(policy.ID, true)

	first := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 130, 4, 4))
	second := f.mustUpload(f.admin, &policy.ID, "b.png", pngBytes(t, 131, 4, 4))
	f.mustUpload(f.admin, &policy.ID, "c.png", pngBytes(t, 132, 4, 4))

	f.mustDelete(f.admin, first.Record.ID)
	f.mustDelete(f.admin, second.Record.ID)
	if row := f.sequenceRow(policy.ID); row.Allocated != 3 || row.Released != "[1,2]" {
		t.Fatalf("释放队列应按释放顺序排列, 实际 %+v", row)
	}

	next := f.mustUpload(f.admin, &policy.ID, "d.png", pngBytes(t, 133, 4, 4))
	if next.Record.RelPath != "1.png" {
		t.Fatalf("应优先复用最早释放的序号, 实际 %q", next.Record.RelPath)
	}
	next = f.mustUpload(f.admin, &policy.ID, "e.png", pngBytes(t, 134, 4, 4))
	if next.Record.RelPath != "2.png" {
		t.Fatalf("应按顺序复用第二个释放序号, 实际 %q", next.Record.RelPath)
	}
	next = f.mustUpload(f.admin, &policy.ID, "f.png", pngBytes(t, 135, 4, 4))
	if next.Record.RelPath != "4.png" {
		t.Fatalf("释放队列为空后应继续分配新号, 实际 %q", next.Record.RelPath)
	}
	if row := f.sequenceRow(policy.ID); row.Allocated != 4 || row.Released != "[]" {
		t.Errorf("复用后的计数器不正确: %+v", row)
	}
}

func TestSharedObjectSequenceReleasedAfterLastReference(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("共享序号", "{seq}.{ext}", model.ConflictRename)
	f.authorize(policy.ID)
	f.setReuseReleasedSeq(policy.ID, true)
	content := pngBytes(t, 140, 4, 4)

	owner := f.mustUpload(f.admin, &policy.ID, "a.png", content)
	bob := f.register("bob")
	reused := f.mustUpload(bob, &policy.ID, "b.png", content)
	if reused.Record.ObjectKey != owner.Record.ObjectKey {
		t.Fatalf("相同内容应复用同一对象: %q %q", reused.Record.ObjectKey, owner.Record.ObjectKey)
	}

	if result := f.mustDelete(f.admin, owner.Record.ID); result.ObjectDeleted {
		t.Fatalf("仍有其他引用时不应删除对象: %+v", result)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[]" {
		t.Fatalf("仍有引用时不应释放序号: %q", row.Released)
	}

	if result := f.mustDelete(bob, reused.Record.ID); !result.ObjectDeleted {
		t.Fatalf("最后一条引用删除时应移除对象: %+v", result)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[1]" {
		t.Fatalf("对象删除后应释放序号: %q", row.Released)
	}

	next := f.mustUpload(f.admin, &policy.ID, "c.png", pngBytes(t, 141, 4, 4))
	if next.Record.RelPath != "1.png" {
		t.Fatalf("开启复用后应重新使用已释放序号, 实际 %q", next.Record.RelPath)
	}
}

func TestTemplateChangeResetsReleasedSequences(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("模板变更", "{seq}.{ext}", model.ConflictRename)
	f.setReuseReleasedSeq(policy.ID, true)

	first := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 150, 4, 4))
	f.mustUpload(f.admin, &policy.ID, "b.png", pngBytes(t, 151, 4, 4))

	const nextTemplate = "img-{seq}.{ext}"
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{PathTemplate: nextTemplate}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("更新路径模板失败: %v", err)
	}
	row := f.sequenceRow(policy.ID)
	if row.PathTemplate != nextTemplate || row.Allocated != 0 || row.Released != "[]" {
		t.Fatalf("模板变化后应重置计数器, 实际 %+v", row)
	}

	// 旧路径不符合新模板, 删除后不应把旧序号写入释放队列.
	if result := f.mustDelete(f.admin, first.Record.ID); !result.ObjectDeleted {
		t.Fatalf("删除应移除外部对象: %+v", result)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[]" {
		t.Errorf("旧模板路径不应释放序号, 实际 %q", row.Released)
	}

	next := f.mustUpload(f.admin, &policy.ID, "c.png", pngBytes(t, 152, 4, 4))
	if next.Record.RelPath != "img-1.png" {
		t.Fatalf("新模板应从 1 开始分配, 实际 %q", next.Record.RelPath)
	}
}

func TestFailedObjectDeleteWaitsForManualRetry(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("删除失败", "{seq}.{ext}", model.ConflictRename)
	f.setReuseReleasedSeq(policy.ID, true)

	uploaded := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 160, 4, 4))
	broken := map[string]string{"endpoint": "http://127.0.0.1:1/dav"}
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{Config: broken}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("更新策略端点失败: %v", err)
	}

	result := f.mustDelete(f.admin, uploaded.Record.ID)
	if result.ObjectDeleted || result.Reason != service.DeleteReasonBackendError {
		t.Fatalf("删除失败应保留外部对象: %+v", result)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[]" {
		t.Fatalf("删除失败不应释放序号: %q", row.Released)
	}

	var task model.MaintenanceTask
	if err := f.database.Where("type = ? AND status = ?", model.TaskObjectDelete, model.TaskFailed).
		Order("id DESC").First(&task).Error; err != nil {
		t.Fatalf("失败任务应保留在数据库中: %v", err)
	}
	if task.LastError == "" {
		t.Error("失败任务应记录错误信息")
	}

	working := map[string]string{"endpoint": f.davEndpoint()}
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{Config: working}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("恢复策略端点失败: %v", err)
	}
	if err := f.svc.Tasks.RunNow(task.ID, f.admin); err != nil {
		t.Fatalf("手动重试失败: %v", err)
	}
	reloaded, err := f.svc.Tasks.Get(task.ID)
	if err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if reloaded.Status != model.TaskDone {
		t.Errorf("手动重试成功后任务应为 done, 实际 %q", reloaded.Status)
	}
	if row := f.sequenceRow(policy.ID); row.Released != "[1]" {
		t.Fatalf("手动重试成功应释放序号: %q", row.Released)
	}

	next := f.mustUpload(f.admin, &policy.ID, "b.png", pngBytes(t, 161, 4, 4))
	if next.Record.RelPath != "1.png" {
		t.Fatalf("重试成功后应复用释放序号, 实际 %q", next.Record.RelPath)
	}
}

func TestDeleteFailedTaskFromQueue(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("删除失败任务", "{seq}.{ext}", model.ConflictRename)

	uploaded := f.mustUpload(f.admin, &policy.ID, "a.png", pngBytes(t, 170, 4, 4))
	broken := map[string]string{"endpoint": "http://127.0.0.1:1/dav"}
	if _, err := f.svc.Policies.Update(policy.ID, service.PolicyInput{Config: broken}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("更新策略端点失败: %v", err)
	}
	if result := f.mustDelete(f.admin, uploaded.Record.ID); result.Reason != service.DeleteReasonBackendError {
		t.Fatalf("删除应失败: %+v", result)
	}

	var task model.MaintenanceTask
	if err := f.database.Where("type = ? AND status = ?", model.TaskObjectDelete, model.TaskFailed).
		Order("id DESC").First(&task).Error; err != nil {
		t.Fatalf("失败任务应保留: %v", err)
	}
	if err := f.svc.Tasks.Delete(task.ID, f.admin); err != nil {
		t.Fatalf("删除失败任务失败: %v", err)
	}
	if _, err := f.svc.Tasks.Get(task.ID); err == nil {
		t.Error("任务删除后应返回不存在")
	}
}

func TestPathPreviewMatchesUpload(t *testing.T) {
	f := newFixture(t)
	preview, err := f.svc.Policies.Preview(service.PreviewInput{
		PathTemplate: "{md5:2}/{md5}.{ext}",
	})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if preview.RelPath != "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png" {
		t.Errorf("预览 rel_path 不正确: %q", preview.RelPath)
	}
	if preview.ObjectKey != "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0.png" {
		t.Errorf("预览 object_key 不正确: %q", preview.ObjectKey)
	}
	if preview.ConflictRelPath != "0f/0f1e2d3c4b5a69788796a5b4c3d2e1f0-1.png" {
		t.Errorf("哈希模板冲突预览应追加 -1, 实际 %q", preview.ConflictRelPath)
	}

	sequencePreview, err := f.svc.Policies.Preview(service.PreviewInput{PathTemplate: "{seq}.{ext}"})
	if err != nil {
		t.Fatalf("递增路径预览失败: %v", err)
	}
	if sequencePreview.RelPath != "1.png" {
		t.Errorf("递增变量预览应从 1 开始, 实际 %q", sequencePreview.RelPath)
	}
	if sequencePreview.ConflictRelPath != "2.png" {
		t.Errorf("序号模板冲突预览应为下一个序号, 实际 %q", sequencePreview.ConflictRelPath)
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

func TestScanPreflightRejectsBadWebDAVCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	f := newFixture(t)
	policy, err := f.svc.Policies.Create(service.PolicyInput{
		Name:           "WebDAV 鉴权失败",
		Type:           "webdav",
		Enabled:        new(false),
		PathTemplate:   "{md5}.{ext}",
		ConflictPolicy: model.ConflictRename,
		Config:         map[string]string{"endpoint": server.URL},
		Secrets:        map[string]string{"username": "wrong", "password": "wrong"},
		URLs: []service.URLInput{
			{Alias: "CDN", URL: "https://cdn.example.com/image/", IsDefault: true},
		},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}
	if _, err := f.svc.Policies.EnqueueScan(context.Background(), policy.ID, f.admin.User.ID, nil, false); err == nil ||
		!strings.Contains(err.Error(), "扫描预检失败") {
		t.Fatalf("鉴权失败扫描错误 = %v, 期望预检失败", err)
	}
	var tasks int64
	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ?", model.TaskPolicyScan).
		Count(&tasks).Error; err != nil {
		t.Fatalf("统计扫描任务失败: %v", err)
	}
	if tasks != 0 {
		t.Fatalf("预检失败不应创建扫描任务, 实际 %d", tasks)
	}
}

func TestTaskQueueScanAndLogPurge(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)

	imagePath := filepath.Join(f.davRoot, "queued.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if err := os.WriteFile(imagePath, pngBytes(t, 91, 4, 4), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}

	if err := f.database.Model(&model.MaintenanceTask{}).
		Where("type = ? AND status = ?", model.TaskPolicyScan, model.TaskPending).
		Update("status", model.TaskDone).Error; err != nil {
		t.Fatalf("清理自动扫描任务失败: %v", err)
	}
	task, err := f.svc.Policies.EnqueueScan(context.Background(), policy.ID, f.admin.User.ID, nil, false)
	if err != nil {
		t.Fatalf("入队扫描失败: %v", err)
	}
	var scanned int64
	if err := f.database.Model(&model.Image{}).Where("policy_id = ? AND object_key = ?", policy.ID, "queued.png").Count(&scanned).Error; err != nil {
		t.Fatalf("统计扫描前记录失败: %v", err)
	}
	if scanned != 0 {
		t.Fatalf("任务入队时不应同步补录文件, 实际 %d", scanned)
	}
	if err := f.svc.Tasks.RunNow(task.ID, f.admin); err != nil {
		t.Fatalf("重新排入扫描任务失败: %v", err)
	}
	var stored model.MaintenanceTask
	if err := f.database.First(&stored, task.ID).Error; err != nil {
		t.Fatalf("读取待处理任务失败: %v", err)
	}
	if stored.Status != model.TaskPending {
		t.Fatalf("重新排入后任务应为 pending: %+v", stored)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		f.svc.Tasks.Start(ctx)
	}()
	defer func() {
		cancel()
		<-workerDone
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := f.database.First(&stored, task.ID).Error; err != nil {
			t.Fatalf("读取后台扫描任务失败: %v", err)
		}
		if stored.Status == model.TaskDone {
			break
		}
		if stored.Status == model.TaskFailed {
			t.Fatalf("后台扫描任务失败: %s", stored.LastError)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待后台扫描任务超时: %+v", stored)
		case <-ticker.C:
		}
	}
	cancel()
	<-workerDone
	if err := f.database.First(&stored, task.ID).Error; err != nil {
		t.Fatalf("读取完成任务失败: %v", err)
	}
	if stored.Status != model.TaskDone {
		t.Fatalf("后台处理后任务应为 done: %+v", stored)
	}
	var scanResult service.ScanResult
	if err := json.Unmarshal([]byte(stored.Result), &scanResult); err != nil {
		t.Fatalf("解析后台扫描结果失败: %v", err)
	}
	if scanResult.Images != 1 || scanResult.Files != 0 {
		t.Fatalf("后台扫描结果统计错误: %+v", scanResult)
	}
	var manifest model.PolicyScanManifest
	if err := f.database.Where("task_id = ?", task.ID).First(&manifest).Error; err != nil {
		t.Fatalf("后台任务应持久化扫描清单: %v", err)
	}
	var manifestKeys []string
	if err := json.Unmarshal([]byte(manifest.ObjectKeys), &manifestKeys); err != nil || len(manifestKeys) != 1 {
		t.Fatalf("扫描清单内容不正确: %v %v", manifestKeys, err)
	}
	if err := f.database.Model(&model.Image{}).Where("policy_id = ? AND object_key = ?", policy.ID, "queued.png").Count(&scanned).Error; err != nil {
		t.Fatalf("统计扫描后记录失败: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("后台任务应补录该文件, 实际 %d", scanned)
	}

	if _, err := f.svc.Tasks.Enqueue(model.TaskLogPurge, nil); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	var logTask model.MaintenanceTask
	if err := f.database.Where("type = ?", model.TaskLogPurge).First(&logTask).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if err := f.svc.Tasks.RunNow(logTask.ID, f.admin); err != nil {
		t.Fatalf("日志清理任务应执行成功: %v", err)
	}
	if err := f.svc.Tasks.RunNow(logTask.ID, f.admin); err != nil {
		t.Fatalf("已完成任务应支持再次执行: %v", err)
	}
	if err := f.database.First(&logTask, logTask.ID).Error; err != nil {
		t.Fatalf("读取重跑后的任务失败: %v", err)
	}
	if logTask.Status != model.TaskDone || logTask.Attempts != 2 {
		t.Fatalf("重跑后的任务应为 done 且尝试次数为 2: %+v", logTask)
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

func TestRegisterEmailAllowlist(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{
		"registration_email_allowlist": "example..com",
	}); !errors.Is(err, apperr.ErrInvalidRequest) {
		t.Fatalf("无效邮箱域名应被拒绝, 实际错误: %v", err)
	}
	if _, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
		Username: "unrestricted",
		Email:    "user@outside.net",
		Password: "password123",
	}, "127.0.0.1", "test"); err != nil {
		t.Fatalf("空白白名单应允许任意邮箱域名: %v", err)
	}

	if err := f.svc.Settings.Update(map[string]string{
		"registration_email_allowlist": "Example.com,corp.test",
	}); err != nil {
		t.Fatalf("设置邮箱白名单失败: %v", err)
	}

	if _, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
		Username: "allowed",
		Email:    "allowed@EXAMPLE.COM",
		Password: "password123",
	}, "127.0.0.1", "test"); err != nil {
		t.Fatalf("白名单域名应允许注册: %v", err)
	}
	for index, email := range []string{"nested@sub.example.com", "outside@example.net"} {
		if _, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
			Username: fmt.Sprintf("blocked%d", index),
			Email:    email,
			Password: "password123",
		}, "127.0.0.1", "test"); !errors.Is(err, apperr.ErrEmailNotAllowed) {
			t.Errorf("非白名单邮箱 %q 应被拒绝, 实际错误: %v", email, err)
		}
	}
}

func TestResendVerificationRateLimitsByUserAndIP(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Settings.Update(map[string]string{
		"smtp_host": "smtp.example.com",
		"smtp_from": "noreply@example.com",
	}); err != nil {
		t.Fatalf("配置 SMTP 失败: %v", err)
	}

	user := &model.User{ID: 1001, Username: "resend", Email: "resend@example.com"}
	for attempt := range 3 {
		if err := f.svc.Auth.ResendVerification(user, "192.0.2.10"); err != nil {
			t.Fatalf("第 %d 次验证邮件请求失败: %v", attempt+1, err)
		}
	}
	if err := f.svc.Auth.ResendVerification(user, "192.0.2.10"); !errors.Is(err, apperr.ErrRateLimited) {
		t.Fatalf("同一账号第 4 次请求应被限流, 实际错误: %v", err)
	}

	for attempt := range 10 {
		ipUser := &model.User{
			ID:       int64(2000 + attempt),
			Username: fmt.Sprintf("resend%d", attempt),
			Email:    fmt.Sprintf("resend%d@example.com", attempt),
		}
		if err := f.svc.Auth.ResendVerification(ipUser, "198.51.100.20"); err != nil {
			t.Fatalf("同一 IP 第 %d 个账号的请求失败: %v", attempt+1, err)
		}
	}
	ipLimitedUser := &model.User{ID: 3000, Username: "last", Email: "last@example.com"}
	if err := f.svc.Auth.ResendVerification(ipLimitedUser, "198.51.100.20"); !errors.Is(err, apperr.ErrRateLimited) {
		t.Fatalf("同一 IP 第 11 次请求应被限流, 实际错误: %v", err)
	}
}

func TestAuthRegisterLoginAndTotp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: " admin ",
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

	user, err := f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "password123"}, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("登录返回用户不正确: %+v", user)
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "wrong"}, "127.0.0.1", "test"); err == nil {
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
	if _, err := f.svc.Auth.SessionPrincipal(token); err != nil {
		t.Fatalf("签发的会话无法解析主体: %v", err)
	}
	if err := f.svc.Auth.Logout(user.ID, "127.0.0.1", "test"); err != nil {
		t.Fatalf("注销会话失败: %v", err)
	}
	if _, err := f.svc.Auth.ParseSession(token); err == nil {
		t.Error("注销后会话令牌应被吊销")
	}
	if _, err := f.svc.Auth.SessionPrincipal(token); err == nil {
		t.Error("注销后会话主体应无法解析")
	}

	secret, otpauthURL, err := f.svc.Auth.TOTPSetup(user.ID)
	if err != nil {
		t.Fatalf("生成 TOTP 失败: %v", err)
	}
	if secret == "" || !strings.HasPrefix(otpauthURL, "otpauth://") {
		t.Fatalf("TOTP 生成结果不正确: %q %q", secret, otpauthURL)
	}
	validCode, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("生成 TOTP 验证码失败: %v", err)
	}
	invalidCode := "000000"
	if invalidCode == validCode {
		invalidCode = "000001"
	}
	if err := f.svc.Auth.TOTPEnable(user.ID, secret, invalidCode, "password123", "127.0.0.1", "test"); err == nil {
		t.Error("错误的 TOTP 验证码应被拒绝")
	}
}
func TestEmailNormalizationAtRegistrationAndAdminCreation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	registered, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: "mixedcase",
		Email:    "  MixedCase@Example.COM ",
		Password: "password123",
	}, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("注册邮箱失败: %v", err)
	}
	if registered.Email != "mixedcase@example.com" {
		t.Fatalf("注册邮箱应保存为小写且去除首尾空格, 实际 %q", registered.Email)
	}
	loggedIn, err := f.svc.Auth.Login(ctx, service.LoginInput{
		Email:    "MIXEDCASE@EXAMPLE.COM",
		Password: "password123",
	}, "127.0.0.1", "test")
	if err != nil || loggedIn.ID != registered.ID {
		t.Fatalf("大小写混合的邮箱应登录到同一账号, 用户=%v, 错误=%v", loggedIn, err)
	}

	groupID := f.normalGID
	created, err := f.svc.Users.Create(service.UserInput{
		Username: "admincreated",
		Email:    "AdminCreated@Example.COM",
		Password: "password123",
		GroupID:  &groupID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("管理员创建用户失败: %v", err)
	}
	if created.Email != "admincreated@example.com" {
		t.Fatalf("管理员创建的邮箱应保存为小写, 实际 %q", created.Email)
	}
	searchResult, err := f.svc.Users.List(service.UserListFilter{Keyword: "ADMINCREATED@EXAMPLE.COM"})
	if err != nil {
		t.Fatalf("按大小写混合邮箱搜索用户失败: %v", err)
	}
	if len(searchResult.Items) != 1 || searchResult.Items[0].ID != created.ID {
		t.Fatalf("邮箱搜索应不区分大小写, 实际结果: %+v", searchResult.Items)
	}

	for _, input := range []service.RegisterInput{
		{Username: "mixedcase", Email: "other@example.com", Password: "password123"},
		{Username: "other", Email: "MIXEDCASE@example.com", Password: "password123"},
	} {
		if _, err := f.svc.Auth.Register(ctx, input, "127.0.0.1", "test"); !errors.Is(err, service.ErrDuplicateRegistration) {
			t.Fatalf("重复用户名或邮箱应返回内部重复注册标记, 实际错误: %v", err)
		}
	}
}

func TestLoginFailureResponsesAreUniform(t *testing.T) {
	f := newFixture(t)
	groupID := f.normalGID
	banned, err := f.svc.Users.Create(service.UserInput{
		Username: "banned",
		Email:    "banned@example.com",
		Password: "password123",
		GroupID:  &groupID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建待封禁用户失败: %v", err)
	}
	bannedStatus := model.UserStatusBanned
	if _, err := f.svc.Users.Update(banned.ID, service.UserInput{Status: &bannedStatus}, f.admin, "127.0.0.1", "test-agent"); err != nil {
		t.Fatalf("封禁测试用户失败: %v", err)
	}

	for _, attempt := range []struct {
		email    string
		password string
	}{
		{email: "missing@example.com", password: "password123"},
		{email: "banned@example.com", password: "password123"},
		{email: "admin@example.com", password: "wrong-password"},
	} {
		_, err := f.svc.Auth.Login(context.Background(), service.LoginInput{
			Email:    attempt.email,
			Password: attempt.password,
		}, "127.0.0.1", "test")
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != "unauthorized" || apiErr.Message != "邮箱或密码错误" {
			t.Errorf("邮箱 %q 的认证失败应统一返回 401 与通用提示, 实际 %v", attempt.email, err)
		}
	}
}

func TestAccountStatusesEnforceReadOnlyGroupTransitions(t *testing.T) {
	f := newFixture(t)
	group, err := f.svc.Groups.Create(service.GroupInput{
		Name:        "状态恢复目标组",
		Permissions: []string{"upload"},
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建恢复目标组失败: %v", err)
	}
	groupID := group.ID
	user, err := f.svc.Users.Create(service.UserInput{
		Username: "stateuser",
		Email:    "stateuser@example.com",
		Password: "password123",
		GroupID:  &groupID,
	}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("创建状态测试用户失败: %v", err)
	}
	var readOnly model.Group
	if err := f.database.Where("name = ? AND is_system = ?", "只读", true).First(&readOnly).Error; err != nil {
		t.Fatalf("读取只读用户组失败: %v", err)
	}

	unverifiedStatus := model.UserStatusUnverified
	unverified, err := f.svc.Users.Update(user.ID, service.UserInput{Status: &unverifiedStatus}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("将用户设为未验证失败: %v", err)
	}
	if unverified.Status != model.UserStatusUnverified || unverified.GroupID != readOnly.ID || unverified.RestoreGroupID == nil || *unverified.RestoreGroupID != groupID || unverified.EmailVerifiedAt != nil {
		t.Fatalf("未验证账号应使用只读组并保留原组: %+v", unverified)
	}
	principal, err := f.svc.Auth.PrincipalFor(user.ID, nil, nil)
	if err != nil {
		t.Fatalf("未验证账号应可登录以完成邮箱验证: %v", err)
	}
	if principal.Group.Name != "只读" || principal.Has(model.PermUpload) {
		t.Fatalf("未验证账号必须使用只读权限, 实际组=%q 权限=%v", principal.Group.Name, principal.Permissions)
	}
	if err := f.svc.Groups.Delete(groupID, f.admin, "127.0.0.1", "test-agent"); err == nil {
		t.Fatal("未验证账号引用的恢复目标组不得删除")
	}

	normalStatus := model.UserStatusNormal
	verified, err := f.svc.Users.Update(user.ID, service.UserInput{Status: &normalStatus}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("恢复正常账号失败: %v", err)
	}
	if verified.Status != model.UserStatusNormal || verified.GroupID != groupID || verified.RestoreGroupID != nil || verified.EmailVerifiedAt == nil {
		t.Fatalf("正常账号应恢复原组并标记邮箱已验证: %+v", verified)
	}
	session, err := f.svc.Auth.IssueSession(user.ID)
	if err != nil {
		t.Fatalf("签发测试会话失败: %v", err)
	}

	bannedStatus := model.UserStatusBanned
	banned, err := f.svc.Users.Update(user.ID, service.UserInput{Status: &bannedStatus}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("封禁用户失败: %v", err)
	}
	if banned.Status != model.UserStatusBanned || banned.GroupID != readOnly.ID || banned.RestoreGroupID == nil || *banned.RestoreGroupID != groupID {
		t.Fatalf("封禁账号应使用只读组并保留原组: %+v", banned)
	}
	if _, err := f.svc.Auth.SessionPrincipal(session); err == nil {
		t.Fatal("封禁账号的既有会话必须失效")
	}
	if _, err := f.svc.Auth.PrincipalFor(user.ID, nil, nil); err == nil {
		t.Fatal("封禁账号不得获得 API 主体")
	}

	restored, err := f.svc.Users.Update(user.ID, service.UserInput{Status: &normalStatus}, f.admin, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("解除封禁失败: %v", err)
	}
	if restored.Status != model.UserStatusNormal || restored.GroupID != groupID || restored.RestoreGroupID != nil {
		t.Fatalf("解除封禁后应恢复封禁前用户组: %+v", restored)
	}
}

func TestAuthLoginTwoStepTotpChallenge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ip := "127.0.0.1"
	userAgent := "test-agent"

	secret, _, err := f.svc.Auth.TOTPSetup(f.admin.User.ID)
	if err != nil {
		t.Fatalf("生成 TOTP 秘钥失败: %v", err)
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("生成 TOTP 验证码失败: %v", err)
	}
	if err := f.svc.Auth.TOTPEnable(f.admin.User.ID, secret, code, "wrong-password", ip, userAgent); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("启用 TOTP 必须验证当前密码, 实际错误: %v", err)
	}
	if err := f.svc.Auth.TOTPEnable(f.admin.User.ID, secret, code, "password123", ip, userAgent); err != nil {
		t.Fatalf("启用 TOTP 失败: %v", err)
	}
	var enabledUser model.User
	if err := f.database.First(&enabledUser, f.admin.User.ID).Error; err != nil {
		t.Fatalf("读取启用后的用户失败: %v", err)
	}
	if enabledUser.TOTPLastCounter <= 0 {
		t.Fatalf("启用验证码计数器未记录: %d", enabledUser.TOTPLastCounter)
	}

	_, err = f.svc.Auth.Login(ctx, service.LoginInput{
		Email:    "admin@example.com",
		Password: "password123",
	}, ip, userAgent)
	var apiErr *apperr.Error
	if !errors.As(err, &apiErr) || apiErr.Details["totp_required"] != true {
		t.Fatalf("首次登录应返回两步验证要求, got %v", err)
	}
	challenge, ok := apiErr.Details["login_challenge"].(string)
	if !ok || challenge == "" {
		t.Fatalf("首次登录未返回验证挑战: %+v", apiErr.Details)
	}
	loginCode, err := totp.GenerateCode(secret, time.Now().UTC().Add(30*time.Second))
	if err != nil {
		t.Fatalf("生成下一周期 TOTP 验证码失败: %v", err)
	}

	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{
		LoginChallenge: challenge,
		TOTPCode:       code,
	}, "192.0.2.1", userAgent); err == nil {
		t.Fatal("不同来源 IP 不应使用该登录挑战")
	}

	wrongCode := "000000"
	if wrongCode == loginCode {
		wrongCode = "000001"
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{
		LoginChallenge: challenge,
		TOTPCode:       wrongCode,
	}, ip, userAgent); err == nil {
		t.Fatal("错误的动态口令应被拒绝")
	}

	code = loginCode
	user, err := f.svc.Auth.Login(ctx, service.LoginInput{
		LoginChallenge: challenge,
		TOTPCode:       code,
	}, ip, userAgent)
	if err != nil {
		t.Fatalf("第二步验证失败: %v", err)
	}
	if user.ID != f.admin.User.ID {
		t.Fatalf("第二步登录返回错误用户: %+v", user)
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{
		LoginChallenge: challenge,
		TOTPCode:       code,
	}, ip, userAgent); err == nil {
		t.Fatal("成功使用过的登录挑战不应重放")
	}

	_, err = f.svc.Auth.Login(ctx, service.LoginInput{
		Email:    "admin@example.com",
		Password: "password123",
	}, ip, userAgent)
	if !errors.As(err, &apiErr) || apiErr.Details["totp_required"] != true {
		t.Fatalf("重放检查登录未返回新的验证挑战: %v", err)
	}
	replayChallenge, ok := apiErr.Details["login_challenge"].(string)
	if !ok || replayChallenge == "" {
		t.Fatalf("重放检查缺少登录挑战: %+v", apiErr.Details)
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{
		LoginChallenge: replayChallenge,
		TOTPCode:       code,
	}, ip, userAgent); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("已消费的动态口令必须拒绝重放, 实际错误: %v", err)
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

func TestScanRejectsUnsafeSubPath(t *testing.T) {
	f := newFixture(t)
	policy := f.createPolicy("扫描校验", "{md5}.{ext}", model.ConflictRename)
	if _, err := f.svc.Policies.Scan(context.Background(), policy.ID, f.admin.User.ID, new("../etc"), false); err == nil {
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
		Where("policy_id = ? AND object_key = ? AND quota_charged = ?", policy.ID, md5RelPath(content), true).
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
		_, lastErr = f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "wrong-password"}, "203.0.113.10", "test")
		if lastErr == nil {
			t.Fatalf("第 %d 次错误密码应失败", attempt+1)
		}
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "wrong-password"}, "203.0.113.10", "test"); err == nil {
		t.Fatal("超过失败上限后应拒绝继续尝试")
	} else {
		var apiErr *apperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "rate_limited" || apiErr.Status != 429 {
			t.Fatalf("超限应返回 rate_limited, 实际 %v", err)
		}
	}

	// 正确密码同样被临时拒绝, 但其他来源不受影响.
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "password123"}, "203.0.113.10", "test"); err == nil {
		t.Error("限流窗口内即使凭据正确也应拒绝")
	}
	if _, err := f.svc.Auth.Login(ctx, service.LoginInput{Email: "admin@example.com", Password: "password123"}, "198.51.100.7", "test"); err != nil {
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
		t.Fatalf("被拒绝的批量删除不应删除记录: %v", err)
	}
}

func TestDanglingDefaultPolicyFallsBack(t *testing.T) {
	f := newFixture(t)
	mainPolicy := f.createPolicy("主存储", "{md5}.{ext}", model.ConflictRename)
	f.authorize(mainPolicy.ID)
	alice := f.register("alice")

	temporary := f.createPolicy("临时策略", "{md5}.{ext}", model.ConflictRename)
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

func TestRegisterRequiresASCIIAlphanumericUsername(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for index, username := range []string{"has-hyphen", "has_underscore", "has.dot", "has space", "夜轻", "name@example.com"} {
		_, err := f.svc.Auth.Register(ctx, service.RegisterInput{
			Username: username,
			Email:    fmt.Sprintf("invalid%d@example.com", index),
			Password: "password123",
		}, "127.0.0.1", "test")
		if err == nil {
			t.Errorf("用户名 %q 应被拒绝", username)
		}
		groupID := f.normalGID
		_, err = f.svc.Users.Create(service.UserInput{
			Username: username,
			Email:    fmt.Sprintf("admininvalid%d@example.com", index),
			Password: "password123",
			GroupID:  &groupID,
		}, f.admin, "127.0.0.1", "test")
		if err == nil {
			t.Errorf("后台创建用户应拒绝用户名 %q", username)
		}
	}

	if _, err := f.svc.Auth.Register(ctx, service.RegisterInput{
		Username: "Az09",
		Email:    "valid@example.com",
		Password: "password123",
	}, "127.0.0.1", "test"); err != nil {
		t.Fatalf("大小写英文字母和数字组成的用户名应有效: %v", err)
	}
}

func TestLoginDoesNotAcceptLegacyUsernameAsEmail(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	legacy := model.User{
		Username:     "victim@example.com",
		Email:        "legacy@example.net",
		PasswordHash: f.admin.User.PasswordHash,
		GroupID:      f.admin.User.GroupID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := f.database.Create(&legacy).Error; err != nil {
		t.Fatalf("写入历史冲突用户名失败: %v", err)
	}

	target, err := f.svc.Auth.Register(context.Background(), service.RegisterInput{
		Username: "victim",
		Email:    "victim@example.com",
		Password: "target-password123",
	}, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("注册邮箱所有者失败: %v", err)
	}
	if _, err := f.svc.Auth.Login(context.Background(), service.LoginInput{
		Email:    "victim",
		Password: "target-password123",
	}, "127.0.0.1", "test"); err == nil {
		t.Fatal("用户名不能用于登录")
	}
	loggedIn, err := f.svc.Auth.Login(context.Background(), service.LoginInput{
		Email:    "victim@example.com",
		Password: "target-password123",
	}, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("邮箱登录失败: %v", err)
	}
	if loggedIn.ID != target.ID {
		t.Fatalf("邮箱登录应解析到邮箱所有者 ID %d, 实际 %d", target.ID, loggedIn.ID)
	}
}
