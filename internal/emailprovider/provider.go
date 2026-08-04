package emailprovider

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"os"
	"strings"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// EmailManager holds named SMTP providers.
type EmailManager struct {
	providers map[string]EmailProviderConfig
}

// New creates an EmailManager from configs.
func New(configs []EmailProviderConfig) *EmailManager {
	m := &EmailManager{providers: make(map[string]EmailProviderConfig, len(configs))}
	for _, cfg := range configs {
		m.providers[cfg.Name] = cfg
		gatewaylog.Default.Info("[EmailProvider] registered", gatewaylog.F("name", cfg.Name))
	}
	return m
}

// Send sends an email via the named provider.
func (m *EmailManager) Send(providerName, to, subject, body string, html bool) error {
	cfg, ok := m.providers[providerName]
	if !ok {
		return fmt.Errorf("email provider %q not found", providerName)
	}
	return sendSMTP(cfg, to, subject, body, html)
}

func sendSMTP(cfg EmailProviderConfig, to, subject, body string, html bool) error {
	from := resolveRef(cfg.FromRef)
	password := resolveRef(cfg.PasswordRef)
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)

	contentType := "text/plain"
	if html {
		contentType = "text/html"
	}
	msg := []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: %s; charset=UTF-8\r\n\r\n%s",
		from, to, subject, contentType, body,
	))

	auth := smtp.PlainAuth("", from, password, cfg.Host)

	switch strings.ToLower(cfg.TLS) {
	case "tls":
		tlsCfg := &tls.Config{ServerName: cfg.Host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp tls dial: %w", err)
		}
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		defer func() { _ = client.Close() }()
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
		if err = client.Mail(from); err != nil {
			return fmt.Errorf("smtp mail from: %w", err)
		}
		if err = client.Rcpt(to); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("smtp data: %w", err)
		}
		defer func() { _ = w.Close() }()
		_, err = w.Write(msg)
		return err
	default: // starttls or none
		return smtp.SendMail(addr, auth, from, []string{to}, msg)
	}
}

func resolveRef(ref string) string {
	if strings.HasPrefix(ref, "env:") {
		return os.Getenv(strings.TrimPrefix(ref, "env:"))
	}
	return ref
}
