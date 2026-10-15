package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mailer 通过 SMTP 发送邮件, 验证邮件使用有界异步队列.
type Mailer struct {
	settings  *Settings
	log       *slog.Logger
	queue     chan mailMessage
	startOnce sync.Once
}

type mailMessage struct {
	to      string
	subject string
	body    string
}

const mailQueueCapacity = 128

// NewMailer 创建邮件服务.
func NewMailer(settings *Settings, log *slog.Logger) *Mailer {
	if log == nil {
		log = slog.Default()
	}
	return &Mailer{
		settings: settings,
		log:      log,
		queue:    make(chan mailMessage, mailQueueCapacity),
	}
}

// Configured 判断 SMTP 是否已配置齐全.
func (m *Mailer) Configured() bool {
	return strings.TrimSpace(m.settings.String("smtp_host")) != "" && strings.TrimSpace(m.settings.String("smtp_from")) != ""
}

// Start 启动异步邮件发送 worker.
func (m *Mailer) Start(ctx context.Context) {
	m.startOnce.Do(func() {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case message := <-m.queue:
					if err := m.Send(message.to, message.subject, message.body); err != nil {
						m.log.Warn("异步发送邮件失败", "error", err)
					}
				}
			}
		}()
	})
}

// Enqueue 将邮件放入有界队列, 不等待 SMTP 网络请求.
func (m *Mailer) Enqueue(to, subject, body string) error {
	if !m.Configured() {
		return errors.New("SMTP 未配置")
	}
	select {
	case m.queue <- mailMessage{to: to, subject: subject, body: body}:
		return nil
	default:
		return errors.New("邮件发送队列已满")
	}
}

// Send 发送纯文本邮件.
func (m *Mailer) Send(to, subject, body string) error {
	host := strings.TrimSpace(m.settings.String("smtp_host"))
	from := strings.TrimSpace(m.settings.String("smtp_from"))
	if host == "" || from == "" {
		return fmt.Errorf("SMTP 未配置: 需要 smtp_host 与 smtp_from")
	}
	port := int(m.settings.Int64("smtp_port", 587))
	if port <= 0 || port > 65535 {
		return fmt.Errorf("SMTP 端口非法: %d", port)
	}
	username := strings.TrimSpace(m.settings.String("smtp_username"))
	password, err := m.settings.Secret("smtp_password")
	if err != nil {
		return fmt.Errorf("SMTP 密码解密失败: %w", err)
	}
	encryption := strings.TrimSpace(strings.ToLower(m.settings.String("smtp_encryption")))
	if encryption == "" {
		encryption = "starttls"
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := m.dial(encryption, host, address)
	if err != nil {
		return err
	}
	defer client.Close()

	if encryption == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("SMTP STARTTLS 失败: %w", err)
			}
		}
	}
	if username != "" {
		auth := smtp.PlainAuth("", username, password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM 失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO 失败: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败: %w", err)
	}
	message := buildMessage(from, to, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		writer.Close()
		return fmt.Errorf("SMTP 写入正文失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("SMTP 发送失败: %w", err)
	}
	return client.Quit()
}

// dial 建立连接, 按加密方式选择隐式 TLS 或明文握手.
func (m *Mailer) dial(encryption, host, address string) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if encryption == "ssl" {
		conn, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, fmt.Errorf("SMTP TLS 连接失败: %w", err)
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("SMTP 握手失败: %w", err)
		}
		return client, nil
	}
	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("SMTP 连接失败: %w", err)
	}
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SMTP 握手失败: %w", err)
	}
	return client, nil
}

// buildMessage 组装纯文本邮件.
func buildMessage(from, to, subject, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return sb.String()
}
