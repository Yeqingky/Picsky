// Package lsky 实现 Lsky Pro v1 API 兼容层: 策略, 上传, 列表, 删除, 虚拟默认相册与资料.
// 兼容层复用与原生接口同一套认证, 权限, 策略白名单, 体积与格式校验, 配额与限流.
package lsky

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yeqingky/Picsky/internal/apperr"
	"github.com/yeqingky/Picsky/internal/clientip"
	"github.com/yeqingky/Picsky/internal/httpupload"
	"github.com/yeqingky/Picsky/internal/middleware"
	"github.com/yeqingky/Picsky/internal/model"
	"github.com/yeqingky/Picsky/internal/service"
)

// LskyDefaultAlbumID 是虚拟默认相册 ID.
const LskyDefaultAlbumID = 1

// LskyDefaultAlbumName 是虚拟默认相册名称.
const LskyDefaultAlbumName = "默认"

// Handler 是兼容层处理器.
type Handler struct {
	svc  *service.Services
	auth *middleware.Auth
	log  *slog.Logger
}

// New 创建兼容层处理器.
func New(svc *service.Services, auth *middleware.Auth, log *slog.Logger) *Handler {
	return &Handler{svc: svc, auth: auth, log: log}
}

// Mount 挂载兼容层路由; prefix 为空表示根模式 (占用 /api/v1).
func (h *Handler) Mount(router *gin.Engine, prefix string) {
	group := router.Group(prefix).Group("/api/v1")

	group.GET("/strategies", h.authRequired(), h.strategies)
	group.POST("/upload", h.upload)
	group.GET("/images", h.authRequired(), h.images)
	group.DELETE("/images/:key", h.authRequired(), h.deleteImage)
	group.GET("/albums", h.authRequired(), h.albums)
	group.DELETE("/albums/:id", h.authRequired(), h.deleteAlbum)
	group.GET("/profile", h.authRequired(), h.profile)
	group.POST("/tokens", h.authRequired(), h.tokensUnsupported("POST /api/v1/tokens"))
	group.DELETE("/tokens", h.authRequired(), h.tokensUnsupported("DELETE /api/v1/tokens"))
}

// envelope 是 Lsky 的响应信封.
type envelope struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// ok 返回成功信封.
func ok(c *gin.Context, data any, message string) {
	if message == "" {
		message = "success"
	}
	c.JSON(http.StatusOK, envelope{Status: true, Message: message, Data: data})
}

// fail 返回业务失败信封 (HTTP 200).
func fail(c *gin.Context, message string) {
	c.JSON(http.StatusOK, envelope{Status: false, Message: message, Data: gin.H{}})
}

// writeError 按 Lsky 语义映射错误.
func (h *Handler) writeError(c *gin.Context, err error) {
	apiErr := apperr.From(err)
	switch apiErr.Status {
	case http.StatusUnauthorized:
		c.JSON(http.StatusUnauthorized, envelope{Status: false, Message: "Authentication failed.", Data: gin.H{}})
	case http.StatusTooManyRequests:
		if apiErr.Details != nil {
			if retry, okCast := apiErr.Details["retry_after_seconds"].(int); okCast {
				c.Header("Retry-After", strconv.Itoa(retry))
			}
		}
		c.JSON(http.StatusTooManyRequests, envelope{Status: false, Message: "Too Many Requests", Data: gin.H{}})
	case http.StatusInternalServerError:
		h.log.Error("兼容层请求失败", "path", c.Request.URL.Path, "error", err)
		fail(c, "服务内部错误")
	default:
		fail(c, apiErr.Message)
	}
}

// authRequired 要求认证: Bearer 头或会话 cookie.
// 上传接口额外接受 multipart 字段 token (见 docs/lsky-compat.md 第 3 节); 为避免密钥进入访问日志, 不接受查询参数.
func (h *Handler) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := h.auth.Resolve(c)
		if err != nil {
			h.writeError(c, err)
			c.Abort()
			return
		}
		if principal == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, envelope{Status: false, Message: "Unauthenticated.", Data: gin.H{}})
			return
		}
		middleware.SetPrincipal(c, principal)
		c.Next()
	}
}

// principalFromToken 用密钥明文解析主体 (兼容只支持表单鉴权的客户端).
func (h *Handler) principalFromToken(token string) (*service.Principal, error) {
	key, err := h.svc.Keys.Authenticate(token)
	if err != nil {
		return nil, err
	}
	return h.svc.Auth.PrincipalFor(key.UserID, &key.ID, key.Scopes)
}

// strategies 返回可用存储策略 (仅 id 与 name).
func (h *Handler) strategies(c *gin.Context) {
	principal := middleware.Principal(c)
	policies, err := h.svc.Policies.AllowedFor(principal)
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]gin.H, 0, len(policies))
	for _, policy := range policies {
		items = append(items, gin.H{"id": policy.ID, "name": policy.Name})
	}
	ok(c, gin.H{"strategies": items}, "")
}

// lskyImage 是 Lsky 图片结构.
type lskyImage struct {
	Album     lskyAlbum `json:"album"`
	Key       int64     `json:"key"`
	Name      string    `json:"name"`
	Pathname  string    `json:"pathname"`
	OriginNum string    `json:"origin_name"`
	Size      float64   `json:"size"`
	Mimetype  string    `json:"mimetype"`
	Extension string    `json:"extension"`
	MD5       string    `json:"md5"`
	SHA1      string    `json:"sha1"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	Links     lskyLinks `json:"links"`
	HumanDate string    `json:"human_date,omitempty"`
	Date      string    `json:"date,omitempty"`
}

// lskyAlbum 是相册结构.
type lskyAlbum struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Intro    string `json:"intro"`
	ImageNum int64  `json:"image_num,omitempty"`
}

// lskyLinks 是图片链接集合.
type lskyLinks struct {
	URL              string `json:"url"`
	HTML             string `json:"html"`
	BBCode           string `json:"bbcode"`
	Markdown         string `json:"markdown"`
	MarkdownWithLink string `json:"markdown_with_link"`
	ThumbnailURL     string `json:"thumbnail_url"`
	DeleteURL        string `json:"delete_url"`
}

// buildLinks 组装链接集合.
func (h *Handler) buildLinks(url, originName string) lskyLinks {
	htmlTag := fmt.Sprintf(`<img src="%s" alt="%s" title="%s" />`, url, originName, originName)
	return lskyLinks{
		URL:              url,
		HTML:             html.EscapeString(htmlTag),
		BBCode:           "[img]" + url + "[/img]",
		Markdown:         fmt.Sprintf("![%s](%s)", originName, url),
		MarkdownWithLink: fmt.Sprintf("[![%s](%s)](%s)", originName, url, url),
		ThumbnailURL:     url,
		DeleteURL:        "",
	}
}

// toLskyImage 把记录转成 Lsky 结构.
func (h *Handler) toLskyImage(c *gin.Context, record *model.Image, withDate bool) lskyImage {
	item := lskyImage{
		Album: lskyAlbum{
			ID:    LskyDefaultAlbumID,
			Name:  LskyDefaultAlbumName,
			Intro: "",
		},
		Key:       record.ID,
		Name:      nameOfPath(record.RelPath),
		Pathname:  record.RelPath,
		OriginNum: record.OriginalFilename,
		Size:      float64(record.Size) / 1024.0,
		Mimetype:  record.MIME,
		Extension: record.Ext,
		MD5:       record.MD5,
		SHA1:      record.SHA1,
		Links:     h.buildLinks(record.URL, record.OriginalFilename),
	}
	if withDate {
		// 与 Lsky 一致: 上传响应不含宽高与时间字段, 列表项才包含.
		if record.Width != nil {
			item.Width = *record.Width
		}
		if record.Height != nil {
			item.Height = *record.Height
		}
		item.Date = h.formatDate(c, record.CreatedAt)
		item.HumanDate = humanize(record.CreatedAt)
	}
	return item
}

// formatDate 按系统设置时区格式化时间.
func (h *Handler) formatDate(c *gin.Context, moment time.Time) string {
	location, err := time.LoadLocation(h.svc.Settings.String("timezone"))
	if err != nil {
		location = time.UTC
	}
	return moment.In(location).Format("2006-01-02 15:04:05")
}

// upload 处理上传.
func (h *Handler) upload(c *gin.Context) {
	principal, err := h.auth.Resolve(c)
	if err != nil {
		h.writeError(c, err)
		return
	}

	principalGroup := middleware.Principal(c)
	if principal == nil && principalGroup != nil {
		principal = principalGroup
	}

	if principal == nil {
		// 只支持表单 token 的客户端需要先读取请求体: 认证前按全局硬上限读取, 体积校验仍由 Upload 按用户组执行.
		result, err := httpupload.Read(c, h.svc.Cfg.TempDir, h.svc.Cfg.UploadMemoryLimit, service.MaxUploadSize)
		if err != nil {
			h.writeError(c, err)
			return
		}
		defer result.Close()
		token := strings.TrimSpace(result.Fields["token"])
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, envelope{Status: false, Message: "Unauthenticated.", Data: gin.H{}})
			return
		}
		principal, err = h.principalFromToken(token)
		if err != nil {
			h.writeError(c, err)
			return
		}
		h.handleParsedUpload(c, principal, result)
		return
	}

	// 认证前无法得知用户组上限: 读取按全局硬上限, 由 Upload 按组上限校验体积.
	result, err := httpupload.Read(c, h.svc.Cfg.TempDir, h.svc.Cfg.UploadMemoryLimit, service.MaxUploadSize)
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer result.Close()
	h.handleParsedUpload(c, principal, result)
}

// handleParsedUpload 处理已解析的 multipart 上传.
func (h *Handler) handleParsedUpload(c *gin.Context, principal *service.Principal, result *httpupload.Result) {
	albumID := strings.TrimSpace(result.Fields["album_id"])
	if albumID != "" && albumID != strconv.Itoa(LskyDefaultAlbumID) {
		fail(c, "选定的相册不存在")
		return
	}

	policyID, err := result.PolicyID("strategy_id")
	if err != nil {
		h.writeError(c, err)
		return
	}

	upload, err := h.svc.Upload.Upload(c.Request.Context(), service.UploadRequest{
		Principal: principal,
		PolicyID:  policyID,
		Spooled:   result.Spooled,
		Source:    model.SourceAPI,
		ClientIP:  clientip.FromContext(c),
		UserAgent: c.GetHeader("User-Agent"),
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	item := h.toLskyImage(c, upload.Record, false)
	ok(c, item, "")
}

// images 返回图片列表.
func (h *Handler) images(c *gin.Context) {
	principal := middleware.Principal(c)

	albumID := strings.TrimSpace(c.Query("album_id"))
	if albumID != "" && albumID != strconv.Itoa(LskyDefaultAlbumID) {
		h.writePaginator(c, []lskyImage{}, 0, 1, h.perPage())
		return
	}

	perPage := h.perPage()
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}

	sortField, sortDesc := lskyOrder(c.Query("order"))
	keyword := c.Query("keyword")
	if keyword == "" {
		keyword = c.Query("q")
	}

	result, err := h.svc.Images.List(principal, service.ListFilter{
		Page:      page,
		PageSize:  perPage,
		Keyword:   keyword,
		SortField: sortField,
		SortDesc:  sortDesc,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]lskyImage, 0, len(result.Items))
	for idx := range result.Items {
		items = append(items, h.toLskyImage(c, &result.Items[idx], true))
	}
	h.writePaginator(c, items, result.Total, page, perPage)
}

// deleteImage 删除图片.
func (h *Handler) deleteImage(c *gin.Context) {
	principal := middleware.Principal(c)
	rawKey := strings.TrimSpace(c.Param("key"))
	id, err := strconv.ParseInt(rawKey, 10, 64)
	if err != nil || id <= 0 {
		fail(c, "图片不存在")
		return
	}

	var record model.Image
	err = h.svc.DB.First(&record, id).Error
	if err != nil {
		h.svc.Audit.Recordf(service.Entry{
			UserID:     &principal.User.ID,
			Action:     "delete_missing",
			TargetType: "image",
			Detail:     map[string]any{"key": rawKey},
			IP:         clientip.FromContext(c),
			UserAgent:  c.GetHeader("User-Agent"),
		})
		ok(c, gin.H{}, "删除成功")
		return
	}

	isOwner := record.UserID == principal.User.ID
	if !isOwner && !principal.Has(model.PermDeleteAll) {
		fail(c, "图片不存在")
		return
	}
	if isOwner && !principal.Has(model.PermDeleteOwn) && !principal.Has(model.PermDeleteAll) {
		fail(c, "没有删除权限")
		return
	}

	result, err := h.svc.Images.Delete(principal, id, clientip.FromContext(c), c.GetHeader("User-Agent"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	if result.ObjectDeleted {
		ok(c, gin.H{}, "删除成功")
		return
	}
	ok(c, gin.H{}, "记录已删除, 外部文件保留")
}

// albums 返回虚拟默认相册.
func (h *Handler) albums(c *gin.Context) {
	principal := middleware.Principal(c)
	var count int64
	if err := h.svc.DB.Model(&model.Image{}).
		Where("user_id = ? AND kind = ?", principal.User.ID, model.KindImage).
		Count(&count).Error; err != nil {
		h.writeError(c, apperr.ErrInternal.WithMessage(err.Error()))
		return
	}
	album := lskyAlbum{ID: LskyDefaultAlbumID, Name: LskyDefaultAlbumName, Intro: "", ImageNum: count}
	h.writePaginator(c, []lskyAlbum{album}, 1, 1, h.perPage())
}

// deleteAlbum 删除相册: 默认相册不可删除, 其余幂等成功.
func (h *Handler) deleteAlbum(c *gin.Context) {
	raw := strings.TrimSpace(c.Param("id"))
	if raw == strconv.Itoa(LskyDefaultAlbumID) {
		fail(c, "默认相册不可删除")
		return
	}
	ok(c, gin.H{}, "删除成功")
}

// profile 返回用户资料与配额.
func (h *Handler) profile(c *gin.Context) {
	principal := middleware.Principal(c)
	user := principal.User

	quota := service.EffectiveQuota(principal.Group, user)
	var capacity any
	var usedCapacity any
	if quota.Mode == model.QuotaModeBytes {
		if quota.Limit != nil {
			capacity = float64(*quota.Limit) / 1024.0
		}
		usedCapacity = float64(quota.Used) / 1024.0
	} else {
		capacity = nil
		usedCapacity = 0
	}

	var imageNum int64
	if err := h.svc.DB.Model(&model.Image{}).
		Where("user_id = ? AND quota_charged = ?", user.ID, true).
		Count(&imageNum).Error; err != nil {
		h.writeError(c, apperr.ErrInternal.WithMessage(err.Error()))
		return
	}

	ok(c, gin.H{
		"name":           user.Username,
		"avatar":         "",
		"email":          user.Email,
		"capacity":       capacity,
		"used_capacity":  usedCapacity,
		"image_num":      imageNum,
		"album_num":      1,
		"url":            h.svc.Settings.String("site_url"),
		"registered_ip":  "",
		"created_at":     user.CreatedAt,
		"email_verified": user.EmailVerifiedAt != nil,
	}, "")
}

// tokensUnsupported 返回 501.
func (h *Handler) tokensUnsupported(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, envelope{
			Status:  false,
			Message: "本服务不提供该接口: " + name,
			Data:    gin.H{},
		})
	}
}

// perPage 取设置项 gallery_page_size.
func (h *Handler) perPage() int {
	size := int(h.svc.Settings.Int64("gallery_page_size", 40))
	if size < 1 {
		size = 40
	}
	return size
}

// lskyOrder 把 Lsky 排序参数映射为服务层排序.
func lskyOrder(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "earliest":
		return "created_at", false
	case "utmost":
		return "size", true
	case "least":
		return "size", false
	default:
		return "created_at", true
	}
}

// nameOfPath 取路径最后一段.
func nameOfPath(relPath string) string {
	segments := strings.Split(relPath, "/")
	return segments[len(segments)-1]
}

// humanize 生成中文相对时间.
func humanize(moment time.Time) string {
	delta := time.Since(moment)
	switch {
	case delta < time.Minute:
		return "刚刚"
	case delta < time.Hour:
		return fmt.Sprintf("%d分钟前", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d小时前", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%d天前", int(delta.Hours()/24))
	case delta < 30*24*time.Hour:
		return fmt.Sprintf("%d周前", int(delta.Hours()/(24*7)))
	case delta < 365*24*time.Hour:
		return fmt.Sprintf("%d个月前", int(delta.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%d年前", int(delta.Hours()/(24*365)))
	}
}

// pageLink 是 Laravel 分页链接项.
type pageLink struct {
	URL    *string `json:"url"`
	Label  string  `json:"label"`
	Active bool    `json:"active"`
}

// paginator 是 Laravel 分页信封.
type paginator struct {
	CurrentPage  int        `json:"current_page"`
	Data         any        `json:"data"`
	FirstPageURL string     `json:"first_page_url"`
	From         *int       `json:"from"`
	LastPage     int        `json:"last_page"`
	LastPageURL  string     `json:"last_page_url"`
	Links        []pageLink `json:"links"`
	NextPageURL  *string    `json:"next_page_url"`
	Path         string     `json:"path"`
	PerPage      int        `json:"per_page"`
	PrevPageURL  *string    `json:"prev_page_url"`
	To           *int       `json:"to"`
	Total        int64      `json:"total"`
}

// writePaginator 输出 Laravel 分页信封.
func (h *Handler) writePaginator(c *gin.Context, data any, total int64, page, perPage int) {
	if perPage < 1 {
		perPage = 40
	}
	lastPage := int((total + int64(perPage) - 1) / int64(perPage))
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}

	base := h.baseURL(c)
	pageURL := func(target int) string {
		query := c.Request.URL.Query()
		query.Set("page", strconv.Itoa(target))
		return base + c.Request.URL.Path + "?" + query.Encode()
	}

	var from, to *int
	if total > 0 {
		start := (page-1)*perPage + 1
		end := start + perPage - 1
		if int64(end) > total {
			end = int(total)
		}
		from, to = &start, &end
	}

	links := make([]pageLink, 0, 8)
	prevLabel := "&laquo; 上一页"
	nextLabel := "下一页 &raquo;"
	if page > 1 {
		url := pageURL(page - 1)
		links = append(links, pageLink{URL: &url, Label: prevLabel, Active: false})
	} else {
		links = append(links, pageLink{URL: nil, Label: prevLabel, Active: false})
	}
	for _, target := range pageWindow(page, lastPage) {
		if target == 0 {
			links = append(links, pageLink{URL: nil, Label: "...", Active: false})
			continue
		}
		url := pageURL(target)
		links = append(links, pageLink{URL: &url, Label: strconv.Itoa(target), Active: target == page})
	}
	if page < lastPage {
		url := pageURL(page + 1)
		links = append(links, pageLink{URL: &url, Label: nextLabel, Active: false})
	} else {
		links = append(links, pageLink{URL: nil, Label: nextLabel, Active: false})
	}

	var nextPage, prevPage *string
	if page < lastPage {
		url := pageURL(page + 1)
		nextPage = &url
	}
	if page > 1 {
		url := pageURL(page - 1)
		prevPage = &url
	}

	firstURL := pageURL(1)
	lastURL := pageURL(lastPage)

	result := paginator{
		CurrentPage:  page,
		Data:         data,
		FirstPageURL: firstURL,
		From:         from,
		LastPage:     lastPage,
		LastPageURL:  lastURL,
		Links:        links,
		NextPageURL:  nextPage,
		Path:         base + c.Request.URL.Path,
		PerPage:      perPage,
		PrevPageURL:  prevPage,
		To:           to,
		Total:        total,
	}
	c.JSON(http.StatusOK, envelope{Status: true, Message: "success", Data: result})
}

// pageWindow 生成分页链接页码窗口, 0 表示省略号.
func pageWindow(page, lastPage int) []int {
	if lastPage <= 12 {
		pages := make([]int, 0, lastPage)
		for i := 1; i <= lastPage; i++ {
			pages = append(pages, i)
		}
		return pages
	}
	pages := []int{1}
	start := page - 2
	if start < 2 {
		start = 2
	}
	end := page + 2
	if end > lastPage-1 {
		end = lastPage - 1
	}
	if start > 2 {
		pages = append(pages, 0)
	}
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	if end < lastPage-1 {
		pages = append(pages, 0)
	}
	pages = append(pages, lastPage)
	return pages
}

// baseURL 返回对外基础地址: 优先站点设置, 否则取请求主机.
func (h *Handler) baseURL(c *gin.Context) string {
	if siteURL := strings.TrimRight(strings.TrimSpace(h.svc.Settings.String("site_url")), "/"); siteURL != "" {
		return siteURL
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}
