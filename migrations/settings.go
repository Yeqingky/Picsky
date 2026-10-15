package migrations

// DefaultSettings 是系统设置默认值, 与 docs/database.md 第 7 节一致.
var DefaultSettings = map[string]string{
	"client_ip_source":                "REMOTE_ADDR",
	"site_name":                       "Picsky",
	"site_url":                        "",
	"timezone":                        "Asia/Shanghai",
	"register_enabled":                "true",
	"email_verification_enabled":      "false",
	"registration_email_allowlist":    "",
	"captcha_provider":                "none",
	"captcha_endpoint":                "",
	"captcha_site_key":                "",
	"captcha_secret":                  "",
	"smtp_host":                       "",
	"smtp_port":                       "587",
	"smtp_username":                   "",
	"smtp_password":                   "",
	"smtp_from":                       "",
	"smtp_encryption":                 "starttls",
	"default_group_id":                "",
	"default_allowed_formats":         "png,jpg,jpeg,gif,webp,bmp,avif,tiff,ico,heic",
	"max_file_size_bytes":             "10485760",
	"default_conflict_policy":         "rename",
	"default_path_template":           "{md5:2}/{md5}.{ext}",
	"delete_object_on_last_reference": "true",
	"gallery_page_size":               "40",
	"log_retention_days":              "90",
}

// SecretSettingKeys 是需要加密存储的设置键.
var SecretSettingKeys = []string{"captcha_secret", "smtp_password"}

// IsSecretSetting 判断设置键是否加密存储.
func IsSecretSetting(key string) bool {
	for _, item := range SecretSettingKeys {
		if item == key {
			return true
		}
	}
	return false
}

// SettingDefinition 描述单个设置项, 供管理后台渲染表单.
type SettingDefinition struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // string / bool / int / password / select / text
	Group   string   `json:"group"`
	Help    string   `json:"help,omitempty"`
	Options []string `json:"options,omitempty"`
}

// SettingDefinitions 是全部设置项的展示元数据.
var SettingDefinitions = []SettingDefinition{
	{Key: "site_name", Label: "站点名称", Type: "string", Group: "站点"},
	{Key: "site_url", Label: "站点对外地址", Type: "string", Group: "站点", Help: "例如 https://picsky.example.com"},
	{Key: "timezone", Label: "时区", Type: "string", Group: "站点", Help: "界面显示与筛选时区, 例如 Asia/Shanghai"},
	{Key: "client_ip_source", Label: "客户端 IP 获取方式", Type: "select", Group: "站点", Help: "默认使用 socket 来源; 代理头仅在可信反向代理覆盖该头且应用端口不对公网开放时使用", Options: []string{"REMOTE_ADDR"}},

	{Key: "register_enabled", Label: "开放注册", Type: "bool", Group: "注册与安全"},
	{Key: "email_verification_enabled", Label: "注册需要邮箱验证", Type: "bool", Group: "注册与安全"},
	{Key: "registration_email_allowlist", Label: "邮箱域名白名单", Type: "string", Group: "注册与安全", Help: "逗号分隔的邮箱域名; 精确匹配; 留空不限制"},
	{Key: "captcha_provider", Label: "人机验证", Type: "select", Group: "注册与安全", Options: []string{"none", "cap", "turnstile"}},
	{Key: "captcha_endpoint", Label: "人机验证服务地址", Type: "string", Group: "注册与安全", Help: "Cap 自建实例地址, 例如 https://cap.example.com; Turnstile 留空"},
	{Key: "captcha_site_key", Label: "人机验证站点 Key", Type: "string", Group: "注册与安全"},
	{Key: "captcha_secret", Label: "人机验证秘钥", Type: "password", Group: "注册与安全", Help: "加密存储; 留空表示不修改"},

	{Key: "smtp_host", Label: "SMTP 服务器", Type: "string", Group: "邮箱"},
	{Key: "smtp_port", Label: "SMTP 端口", Type: "int", Group: "邮箱"},
	{Key: "smtp_username", Label: "SMTP 用户名", Type: "string", Group: "邮箱"},
	{Key: "smtp_password", Label: "SMTP 密码", Type: "password", Group: "邮箱", Help: "加密存储; 留空表示不修改"},
	{Key: "smtp_from", Label: "发件人地址", Type: "string", Group: "邮箱"},
	{Key: "smtp_encryption", Label: "SMTP 加密方式", Type: "select", Group: "邮箱", Options: []string{"starttls", "ssl", "none"}},

	{Key: "default_group_id", Label: "新用户默认用户组", Type: "int", Group: "上传限制", Help: "用户组 ID; 留空使用标记为默认的用户组"},
	{Key: "default_allowed_formats", Label: "系统格式基线", Type: "string", Group: "上传限制", Help: "逗号分隔; 全局生效 (不区分用户组); SVG 永远禁止"},
	{Key: "max_file_size_bytes", Label: "单文件上限 (字节)", Type: "int", Group: "上传限制"},
	{Key: "gallery_page_size", Label: "图库每页数量", Type: "int", Group: "上传限制"},

	{Key: "default_conflict_policy", Label: "新策略默认冲突策略", Type: "select", Group: "存储默认", Options: []string{"rename", "skip", "overwrite", "fail"}},
	{Key: "default_path_template", Label: "新策略默认魔法路径", Type: "string", Group: "存储默认"},
	{Key: "delete_object_on_last_reference", Label: "引用归零时删除外部对象", Type: "bool", Group: "存储默认", Help: "仅在策略后端声明 delete 能力时生效"},

	{Key: "log_retention_days", Label: "审计日志保留天数", Type: "int", Group: "维护"},
}
