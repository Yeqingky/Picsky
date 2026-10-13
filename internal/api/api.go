// Package api 提供原生 REST 接口与前端静态资源托管.
package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/lsky"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
	"github.com/yeqingky/Picsky/internal/storage"
	"github.com/yeqingky/Picsky/internal/web"
)

// Server 组装路由与处理器.
type Server struct {
	svc  *service.Services
	cfg  *config.Config
	log  *slog.Logger
	auth *middleware.Auth
}

// New 创建 API 服务器.
func New(svc *service.Services, cfg *config.Config, log *slog.Logger) *Server {
	return &Server{
		svc:  svc,
		cfg:  cfg,
		log:  log,
		auth: middleware.NewAuth(svc),
	}
}

// Register 注册全部路由.
func (s *Server) Register(router *gin.Engine) {
	router.Use(s.recoverer(), s.limitBody())

	group := router.Group(s.cfg.APIPrefix)
	group.GET("/health", s.health)
	group.GET("/site", s.publicSite)

	authGroup := group.Group("/auth")
	authGroup.POST("/login", s.login)
	authGroup.POST("/register", s.register)
	authGroup.GET("/captcha", s.captchaPublic)
	authGroup.POST("/verify-email", s.verifyEmail)
	authGroup.POST("/password/reset/request", s.requestPasswordReset)
	authGroup.POST("/password/reset", s.resetPassword)

	authed := authGroup.Group("", s.auth.Required())
	authed.POST("/logout", s.logout)
	authed.GET("/me", s.me)
	authed.PATCH("/password", s.changePassword)
	authed.PATCH("/profile", s.updateProfile)
	authed.POST("/totp/setup", s.totpSetup)
	authed.POST("/totp/enable", s.totpEnable)
	authed.POST("/totp/disable", s.totpDisable)
	authed.POST("/resend-verification", s.resendVerification)

	user := group.Group("", s.auth.Required())
	user.POST("/upload", s.upload)
	user.POST("/upload/check", s.uploadCheck)
	user.POST("/upload/base64", s.uploadBase64)
	user.GET("/images", s.listImages)
	user.GET("/images/:id", s.getImage)
	user.PATCH("/images/:id", s.patchImage)
	user.DELETE("/images/:id", s.deleteImage)
	user.POST("/images/batch-delete", s.batchDeleteImages)
	user.GET("/policies", s.userPolicies)

	keys := user.Group("/keys", middleware.Require(model.PermManageKeys))
	keys.GET("", s.listKeys)
	keys.POST("", s.createKey)
	keys.DELETE("/:id", s.revokeKey)

	admin := group.Group("/admin", s.auth.Required(), s.requireAdminArea())
	admin.GET("/stats", s.adminStats)

	users := admin.Group("/users", middleware.Require(model.PermManageUsers))
	users.GET("", s.adminListUsers)
	users.POST("", s.adminCreateUser)
	users.PATCH("/:id", s.adminUpdateUser)
	users.DELETE("/:id", s.adminDeleteUser)

	groups := admin.Group("/groups", middleware.Require(model.PermManageGroups))
	groups.GET("", s.adminListGroups)
	groups.POST("", s.adminCreateGroup)
	groups.PATCH("/:id", s.adminUpdateGroup)
	groups.DELETE("/:id", s.adminDeleteGroup)
	groups.PUT("/:id/policies", s.adminSetGroupPolicies)

	policies := admin.Group("/policies", middleware.Require(model.PermManagePolicies))
	policies.GET("", s.adminListPolicies)
	policies.POST("", s.adminCreatePolicy)
	policies.POST("/preview", s.adminPreviewPolicy)
	policies.GET("/drivers", s.adminPolicyDrivers)
	policies.PATCH("/:id", s.adminUpdatePolicy)
	policies.DELETE("/:id", s.adminDeletePolicy)
	policies.POST("/:id/test", s.adminTestPolicy)
	policies.POST("/:id/scan", s.adminScanPolicy)

	settings := admin.Group("/settings", middleware.Require(model.PermManageSettings))
	settings.GET("", s.adminGetSettings)
	settings.PATCH("", s.adminUpdateSettings)

	tasks := admin.Group("/tasks", middleware.Require(model.PermManageSettings))
	tasks.GET("", s.adminListTasks)
	tasks.POST("/:id/run", s.adminRunTask)

	logs := admin.Group("/logs", middleware.Require(model.PermViewLogs))
	logs.GET("", s.adminListLogs)

	if s.cfg.LskyCompatEnabled {
		lsky.New(s.svc, s.auth, s.log).Mount(router, s.cfg.LskyCompatPrefix)
	}

	s.mountFrontend(router)
}

// requireAdminArea 限制后台区域入口: 具备任一管理权限位才可访问.
func (s *Server) requireAdminArea() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal := middleware.Principal(c)
		if principal == nil {
			c.AbortWithStatusJSON(apperr.ErrUnauthorized.Status, apperr.ErrUnauthorized)
			return
		}
		if !principal.IsAdmin() {
			c.AbortWithStatusJSON(apperr.ErrForbidden.Status, apperr.ErrForbidden.WithMessage("没有后台访问权限"))
			return
		}
		c.Next()
	}
}

// maxJSONBodyBytes 是默认的 JSON 请求体上限.
const maxJSONBodyBytes = 1 << 20

// limitBody 限制非 multipart 请求体大小: 上传接口自行按策略上限限制, 其余接口使用固定上限.
func (s *Server) limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := c.GetHeader("Content-Type")
		path := c.Request.URL.Path
		if strings.HasPrefix(contentType, "multipart/form-data") || strings.HasSuffix(path, "/upload/base64") {
			c.Next()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBodyBytes)
		}
		c.Next()
	}
}

// recoverer 捕获 panic 并返回内部错误.
func (s *Server) recoverer() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		s.log.Error("请求处理发生 panic", "path", c.Request.URL.Path, "panic", recovered)
		c.AbortWithStatusJSON(http.StatusInternalServerError, apperr.ErrInternal)
	})
}

// fail 输出统一错误结构.
func (s *Server) fail(c *gin.Context, err error) {
	apiErr := apperr.From(err)
	if apiErr.Status >= http.StatusInternalServerError {
		s.log.Error("请求处理失败", "path", c.Request.URL.Path, "method", c.Request.Method, "error", err)
	}
	if apiErr.Status == http.StatusTooManyRequests && apiErr.Details != nil {
		if retry, ok := apiErr.Details["retry_after_seconds"].(int); ok {
			c.Header("Retry-After", strconv.Itoa(retry))
		}
	}
	c.JSON(apiErr.Status, apiErr)
}

// health 运维探活.
func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": config.Version})
}

// publicSite 返回站点公开信息 (名称, 注册开关, 找回密码可用性与人机验证配置).
func (s *Server) publicSite(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"site_name":              s.svc.Settings.String("site_name"),
		"register_enabled":       s.svc.Settings.Bool("register_enabled", true),
		"password_reset_enabled": s.svc.Mailer.Configured(),
		"captcha":                s.svc.Captcha.Public(),
	})
}

// mountFrontend 托管前端静态资源; 非 API 路径回落到 index.html.
func (s *Server) mountFrontend(router *gin.Engine) {
	assets, err := web.FS()
	if err != nil {
		s.log.Warn("前端资源不可用", "error", err)
	}
	fileServer := http.FileServer(http.FS(assets))

	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, s.cfg.APIPrefix) || s.isLskyPath(path) {
			c.JSON(http.StatusNotFound, apperr.ErrNotFound)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.JSON(http.StatusNotFound, apperr.ErrNotFound)
			return
		}
		if !web.Available() {
			c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", []byte(frontendMissingPage))
			return
		}
		if assets != nil && path != "/" {
			if file, err := assets.Open(strings.TrimPrefix(path, "/")); err == nil {
				file.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		index, err := web.IndexHTML()
		if err != nil {
			c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", []byte(frontendMissingPage))
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

// isLskyPath 判断路径是否属于兼容层 (根模式下兼容层占用 /api/v1).
func (s *Server) isLskyPath(path string) bool {
	if !s.cfg.LskyCompatEnabled || s.cfg.LskyCompatPrefix == "" {
		return false
	}
	return strings.HasPrefix(path, s.cfg.LskyCompatPrefix+"/") || path == s.cfg.LskyCompatPrefix
}

const frontendMissingPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>Picsky</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 720px; margin: 64px auto; line-height: 1.7">
<h1>Picsky 后端已启动</h1>
<p>前端产物尚未构建, 因此无法显示 WebUI. 请在 web/ 目录执行:</p>
<pre style="background:#f4f4f4;padding:12px;border-radius:6px">npm install
npm run build</pre>
<p>然后重新构建后端 (<code>go build -o picsky ./cmd/picsky</code>) 或使用 Docker 镜像.</p>
<p>API 已可用: <code>GET /api/v1/health</code></p>
</body>
</html>`

// urlDTO 是分发域名别名条目.
type urlDTO struct {
	Alias     string `json:"alias"`
	URL       string `json:"url"`
	IsDefault bool   `json:"is_default"`
}

// policyMeta 缓存策略的名称与分发域名.
type policyMeta struct {
	Name string
	URLs []model.PolicyURL
}

// imageDTO 是图片记录的下发结构.
type imageDTO struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	PolicyID         int64     `json:"policy_id"`
	PolicyName       string    `json:"policy_name"`
	Kind             string    `json:"kind"`
	RelPath          string    `json:"rel_path"`
	ObjectKey        string    `json:"object_key"`
	URL              string    `json:"url"`
	URLs             []urlDTO  `json:"urls"`
	MD5              string    `json:"md5"`
	SHA1             string    `json:"sha1"`
	SHA256           string    `json:"sha256"`
	SHA512           string    `json:"sha512"`
	Size             int64     `json:"size"`
	Format           string    `json:"format"`
	Ext              string    `json:"ext"`
	MIME             string    `json:"mime"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	OriginalFilename string    `json:"original_filename"`
	QuotaCharged     bool      `json:"quota_charged"`
	Source           string    `json:"source"`
	ClientIP         string    `json:"client_ip"`
	CreatedAt        time.Time `json:"created_at"`
}

// hashDTO 是上传响应中的哈希集合.
type hashDTO struct {
	MD5    string `json:"md5"`
	SHA256 string `json:"sha256"`
	SHA512 string `json:"sha512"`
}

// uploadDTO 是上传响应.
type uploadDTO struct {
	ID               int64     `json:"id"`
	PolicyID         int64     `json:"policy_id"`
	RelPath          string    `json:"rel_path"`
	ObjectKey        string    `json:"object_key"`
	URL              string    `json:"url"`
	URLs             []urlDTO  `json:"urls"`
	Hashes           hashDTO   `json:"hashes"`
	Size             int64     `json:"size"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	Format           string    `json:"format"`
	Ext              string    `json:"ext"`
	MIME             string    `json:"mime"`
	OriginalFilename string    `json:"original_filename"`
	Dedup            bool      `json:"dedup"`
	QuotaCharged     bool      `json:"quota_charged"`
	CreatedAt        time.Time `json:"created_at"`
}

// metaFor 批量加载策略元数据.
func (s *Server) metaFor(policyIDs []int64) (map[int64]*policyMeta, error) {
	metas := map[int64]*policyMeta{}
	unique := make([]int64, 0, len(policyIDs))
	seen := map[int64]bool{}
	for _, id := range policyIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return metas, nil
	}
	var policies []model.StoragePolicy
	if err := s.svc.DB.Where("id IN ?", unique).Find(&policies).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	var urls []model.PolicyURL
	if err := s.svc.DB.Where("policy_id IN ?", unique).Order("is_default DESC, sort ASC, id ASC").Find(&urls).Error; err != nil {
		return nil, apperr.ErrInternal.WithMessage(err.Error())
	}
	for _, policy := range policies {
		metas[policy.ID] = &policyMeta{Name: policy.Name, URLs: []model.PolicyURL{}}
	}
	for _, url := range urls {
		if meta, ok := metas[url.PolicyID]; ok {
			meta.URLs = append(meta.URLs, url)
		}
	}
	return metas, nil
}

// urlsFor 拼接策略域名与相对路径.
func (s *Server) urlsFor(meta *policyMeta, relPath string) []urlDTO {
	urls := make([]urlDTO, 0, len(meta.URLs))
	for _, item := range meta.URLs {
		urls = append(urls, urlDTO{
			Alias:     item.Alias,
			URL:       storage.JoinURL(item.URL, relPath),
			IsDefault: item.IsDefault,
		})
	}
	return urls
}

// toImageDTO 组装单条记录.
func (s *Server) toImageDTO(record *model.Image, meta *policyMeta) imageDTO {
	dto := imageDTO{
		ID:               record.ID,
		UserID:           record.UserID,
		PolicyID:         record.PolicyID,
		Kind:             record.Kind,
		RelPath:          record.RelPath,
		ObjectKey:        record.ObjectKey,
		URL:              record.URL,
		URLs:             []urlDTO{},
		MD5:              record.MD5,
		SHA1:             record.SHA1,
		SHA256:           record.SHA256,
		SHA512:           record.SHA512,
		Size:             record.Size,
		Format:           record.Format,
		Ext:              record.Ext,
		MIME:             record.MIME,
		Width:            record.Width,
		Height:           record.Height,
		OriginalFilename: record.OriginalFilename,
		QuotaCharged:     record.QuotaCharged,
		Source:           record.Source,
		ClientIP:         record.ClientIP,
		CreatedAt:        record.CreatedAt,
	}
	if meta != nil {
		dto.PolicyName = meta.Name
		dto.URLs = s.urlsFor(meta, record.RelPath)
	}
	return dto
}

// toImageDTOs 组装记录列表.
func (s *Server) toImageDTOs(records []model.Image) ([]imageDTO, error) {
	policyIDs := make([]int64, 0, len(records))
	for _, record := range records {
		policyIDs = append(policyIDs, record.PolicyID)
	}
	metas, err := s.metaFor(policyIDs)
	if err != nil {
		return nil, err
	}
	dtos := make([]imageDTO, 0, len(records))
	for idx := range records {
		dtos = append(dtos, s.toImageDTO(&records[idx], metas[records[idx].PolicyID]))
	}
	return dtos, nil
}

// toUploadDTO 组装上传响应.
func (s *Server) toUploadDTO(result *service.UploadResult) uploadDTO {
	record := result.Record
	meta := &policyMeta{Name: "", URLs: result.URLs}
	return uploadDTO{
		ID:               record.ID,
		PolicyID:         record.PolicyID,
		RelPath:          record.RelPath,
		ObjectKey:        record.ObjectKey,
		URL:              record.URL,
		URLs:             s.urlsFor(meta, record.RelPath),
		Hashes:           hashDTO{MD5: record.MD5, SHA256: record.SHA256, SHA512: record.SHA512},
		Size:             record.Size,
		Width:            record.Width,
		Height:           record.Height,
		Format:           record.Format,
		Ext:              record.Ext,
		MIME:             record.MIME,
		OriginalFilename: record.OriginalFilename,
		Dedup:            result.Dedup,
		QuotaCharged:     result.QuotaCharged,
		CreatedAt:        record.CreatedAt,
	}
}

// pagination 读取分页参数.
func pagination(c *gin.Context, defaultSize, maxSize int) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultSize)))
	if size < 1 {
		size = defaultSize
	}
	if maxSize > 0 && size > maxSize {
		size = maxSize
	}
	return page, size
}

// idParam 读取路径中的整数 ID.
func idParam(c *gin.Context, name string) (int64, error) {
	raw := c.Param(name)
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, apperr.ErrInvalidRequest.WithMessage(name + " 必须是正整数")
	}
	return parsed, nil
}

// timeParam 解析时间过滤参数, 支持 RFC3339 与 yyyy-MM-dd.
func timeParam(c *gin.Context, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	return nil, apperr.ErrInvalidRequest.WithMessage(name + " 需要 RFC3339 或 yyyy-MM-dd 格式")
}

// clientIP 返回请求来源地址.
func clientIP(c *gin.Context) string { return c.ClientIP() }

// serviceEntry 构造审计日志条目.
func serviceEntry(principal *service.Principal, action, targetType string, targetID int64, detail map[string]any, ip, userAgentValue string) service.Entry {
	entry := service.Entry{
		Action:     action,
		TargetType: targetType,
		Detail:     detail,
		IP:         ip,
		UserAgent:  userAgentValue,
	}
	if principal != nil && principal.User != nil {
		id := principal.User.ID
		entry.UserID = &id
	}
	if targetID > 0 {
		id := targetID
		entry.TargetID = &id
	}
	return entry
}

// userAgent 返回请求 UA (截断).
func userAgent(c *gin.Context) string {
	agent := c.GetHeader("User-Agent")
	if len(agent) > 255 {
		return agent[:255]
	}
	return agent
}
