package mail

import (
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
	}
	if cfg.MailOptions.SMTP != nil {
		mc.Host = cfg.MailOptions.SMTP.Options.Host
		mc.Port = cfg.MailOptions.SMTP.Options.Port
		mc.User = cfg.MailOptions.SMTP.User
		mc.Pass = cfg.MailOptions.SMTP.Pass
	}
	if cfg.MailOptions.Resend != nil && cfg.MailOptions.Resend.APIKey != "" {
		mc.UseResend = true
		mc.ResendKey = cfg.MailOptions.Resend.APIKey
	}
	return mc
}
