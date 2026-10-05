package mail

import (
	"strings"

	"github.com/mx-space/core/internal/config"
)

// BuildMailConfig constructs a mail.Config from the application's FullConfig.
// This centralises the mapping logic so that every caller (subscribe, notify, etc.)
// builds the mailer configuration consistently.
func BuildMailConfig(cfg *config.FullConfig) Config {
	if cfg == nil {
		return Config{}
	}
	mc := Config{
		Enable: cfg.MailOptions.Enable,
		From:   cfg.MailOptions.From,
		Name:   strings.TrimSpace(cfg.SEO.Title),
	}
	if mc.Name == "" {
		mc.Name = "Mx Space"
	}
	if cfg.MailOptions.SMTP != nil {
		mc.Host = cfg.MailOptions.SMTP.Options.Host
		mc.Port = cfg.MailOptions.SMTP.Options.Port
		mc.Secure = cfg.MailOptions.SMTP.Options.Secure
		mc.User = cfg.MailOptions.SMTP.User
		mc.Pass = cfg.MailOptions.SMTP.Pass
		if cfg.MailOptions.SMTP.Options.Socks5 != nil {
			mc.Socks5 = &SOCKS5ProxyConfig{
				Enable: cfg.MailOptions.SMTP.Options.Socks5.Enable,
				Host:   cfg.MailOptions.SMTP.Options.Socks5.Host,
				Port:   cfg.MailOptions.SMTP.Options.Socks5.Port,
				User:   cfg.MailOptions.SMTP.Options.Socks5.User,
				Pass:   cfg.MailOptions.SMTP.Options.Socks5.Pass,
			}
		}
	}
	// The provider selected in the panel wins; a leftover Resend key must not hijack SMTP.
	// Configs without a provider (older ones) keep using Resend whenever a key is set.
	provider := strings.ToLower(strings.TrimSpace(cfg.MailOptions.Provider))
	if cfg.MailOptions.Resend != nil && cfg.MailOptions.Resend.APIKey != "" && provider != "smtp" {
		mc.UseResend = true
		mc.ResendKey = cfg.MailOptions.Resend.APIKey
	}
	return mc
}
