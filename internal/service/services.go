package service

import (
	"log/slog"

	"gorm.io/gorm"

	"github.com/yeqingky/Picsky/internal/config"
	"github.com/yeqingky/Picsky/internal/crypto"
)

// Services 汇总全部业务服务, 由入口统一装配.
type Services struct {
	DB       *gorm.DB
	Cfg      *config.Config
	Cipher   *crypto.Cipher
	Log      *slog.Logger
	Settings *Settings
	Audit    *Audit
	Captcha  *Captcha
	Mailer   *Mailer
	Keys     *Keys
	Auth     *Auth
	Policies *Policies
	Images   *Images
	Upload   *Upload
	Groups   *Groups
	Users    *Users
	Tasks    *Tasks
	Stats    *Stats
}

// New 装配全部服务.
func New(cfg *config.Config, database *gorm.DB, log *slog.Logger) (*Services, error) {
	cipher, err := crypto.New(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	settings, err := NewSettings(database, cipher)
	if err != nil {
		return nil, err
	}

	audit := NewAudit(database)
	captcha := NewCaptcha(settings)
	mailer := NewMailer(settings, log)
	keys := NewKeys(database, cfg.SecretKey)
	auth := NewAuth(database, settings, cipher, audit, captcha, mailer, cfg.SecretKey, log)
	policies := NewPolicies(database, cfg, settings, cipher, audit, log)
	images := NewImages(database, settings, audit, policies, log)
	upload := NewUpload(database, cfg, settings, audit, policies, log)
	groups := NewGroups(database, settings, audit)
	users := NewUsers(database, settings, audit, images, auth)
	tasks := NewTasks(database, settings, audit, policies, images, log)
	policies.SetTasks(tasks)
	images.SetTasks(tasks)
	stats := NewStats(database, settings)

	return &Services{
		DB:       database,
		Cfg:      cfg,
		Cipher:   cipher,
		Log:      log,
		Settings: settings,
		Audit:    audit,
		Captcha:  captcha,
		Mailer:   mailer,
		Keys:     keys,
		Auth:     auth,
		Policies: policies,
		Images:   images,
		Upload:   upload,
		Groups:   groups,
		Users:    users,
		Tasks:    tasks,
		Stats:    stats,
	}, nil
}
