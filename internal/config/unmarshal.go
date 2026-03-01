package config

import (
	"encoding/json"
	"strings"
)

func (s SMTPConfig) MarshalJSON() ([]byte, error) {
	host := strings.TrimSpace(s.Options.Host)
	port := s.Options.Port
	if port == 0 {
		port = 465
	}
	secure := s.Options.Secure

	return json.Marshal(struct {
		User    string      `json:"user"`
		Pass    string      `json:"pass"`
		Host    string      `json:"host"`
		Port    int         `json:"port"`
		Secure  bool        `json:"secure"`
		Options SMTPOptions `json:"options"`
	}{
		User:   strings.TrimSpace(s.User),
		Pass:   s.Pass,
		Host:   host,
		Port:   port,
		Secure: secure,
		Options: SMTPOptions{
			Host:   host,
			Port:   port,
			Secure: secure,
		},
	})
}

func (s *SMTPConfig) UnmarshalJSON(data []byte) error {
	next := *s
	if next.Options.Port == 0 {
		next.Options.Port = 465
	}

	var raw struct {
		User    string `json:"user"`
		Pass    string `json:"pass"`
		Options *struct {
			Host   string `json:"host"`
			Port   int    `json:"port"`
			Secure *bool  `json:"secure"`
		} `json:"options"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Secure *bool  `json:"secure"`
		Auth   *struct {
			User string `json:"user"`
			Pass string `json:"pass"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.User != "" {
		next.User = strings.TrimSpace(raw.User)
	}
	if raw.Pass != "" {
		next.Pass = raw.Pass
	}
	if raw.Auth != nil {
		if next.User == "" {
			next.User = strings.TrimSpace(raw.Auth.User)
		}
		if next.Pass == "" {
			next.Pass = raw.Auth.Pass
		}
	}

	if raw.Options != nil {
		next.Options.Host = strings.TrimSpace(raw.Options.Host)
		if raw.Options.Port != 0 {
			next.Options.Port = raw.Options.Port
		}
		if raw.Options.Secure != nil {
			next.Options.Secure = *raw.Options.Secure
		}
	} else {
		if strings.TrimSpace(raw.Host) != "" {
			next.Options.Host = strings.TrimSpace(raw.Host)
		}
		if raw.Port != 0 {
			next.Options.Port = raw.Port
		}
		if raw.Secure != nil {
			next.Options.Secure = *raw.Secure
		}
	}

	if next.Options.Port == 0 {
		next.Options.Port = 465
	}
	*s = next
	return nil
}

func (o *FriendLinkOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		AllowApply                  *bool `json:"allow_apply"`
		AllowSubPath                *bool `json:"allow_sub_path"`
		EnableAvatarInternalization *bool `json:"enable_avatar_internalization"`
		AvatarInternationalization  *bool `json:"avatar_internationalization"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.AllowApply != nil {
		next.AllowApply = *raw.AllowApply
	}
	if raw.AllowSubPath != nil {
		next.AllowSubPath = *raw.AllowSubPath
	}
	if raw.EnableAvatarInternalization != nil {
		next.EnableAvatarInternalization = *raw.EnableAvatarInternalization
	} else if raw.AvatarInternationalization != nil {
		next.EnableAvatarInternalization = *raw.AvatarInternationalization
	}

	*o = next
	return nil
}

func (o *ImageBedOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Enable         *bool       `json:"enable"`
		Path           *string     `json:"path"`
		AllowedFormats interface{} `json:"allowed_formats"`
		MaxSizeMB      *int        `json:"max_size_mb"`
		MaxSize        *int        `json:"max_size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Enable != nil {
		next.Enable = *raw.Enable
	}
	if raw.Path != nil {
		next.Path = *raw.Path
	}
	if raw.AllowedFormats != nil {
		switch val := raw.AllowedFormats.(type) {
		case string:
			next.AllowedFormats = strings.TrimSpace(val)
		case []interface{}:
			items := make([]string, 0, len(val))
			for _, item := range val {
				s, ok := item.(string)
				if !ok {
					continue
				}
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				items = append(items, s)
			}
			next.AllowedFormats = strings.Join(items, ",")
		}
	}
	if raw.MaxSizeMB != nil {
		next.MaxSizeMB = *raw.MaxSizeMB
	} else if raw.MaxSize != nil {
		next.MaxSizeMB = *raw.MaxSize
	}

	*o = next
	return nil
}

func (o *ImageStorageOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Enable               *bool   `json:"enable"`
		SyncOnPublish        *bool   `json:"sync_on_publish"`
		DeleteLocalAfterSync *bool   `json:"delete_local_after_sync"`
		AutoDeleteAfterSync  *bool   `json:"auto_delete_after_sync"`
		Endpoint             *string `json:"endpoint"`
		SecretID             *string `json:"secret_id"`
		SecretKey            *string `json:"secret_key"`
		Bucket               *string `json:"bucket"`
		Region               *string `json:"region"`
		CustomDomain         *string `json:"custom_domain"`
		Prefix               *string `json:"prefix"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Enable != nil {
		next.Enable = *raw.Enable
	}
	if raw.SyncOnPublish != nil {
		next.SyncOnPublish = *raw.SyncOnPublish
	}
	if raw.DeleteLocalAfterSync != nil {
		next.DeleteLocalAfterSync = *raw.DeleteLocalAfterSync
	} else if raw.AutoDeleteAfterSync != nil {
		next.DeleteLocalAfterSync = *raw.AutoDeleteAfterSync
	}
	if raw.Endpoint != nil {
		next.Endpoint = raw.Endpoint
	}
	if raw.SecretID != nil {
		next.SecretID = raw.SecretID
	}
	if raw.SecretKey != nil {
		next.SecretKey = raw.SecretKey
	}
	if raw.Bucket != nil {
		next.Bucket = raw.Bucket
	}
	if raw.Region != nil {
		next.Region = *raw.Region
	}
	if raw.CustomDomain != nil {
		next.CustomDomain = *raw.CustomDomain
	}
	if raw.Prefix != nil {
		next.Prefix = *raw.Prefix
	}

	*o = next
	return nil
}

func (o *TextOptions) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		Macros       *bool `json:"macros"`
		MacroEnabled *bool `json:"macro_enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Macros != nil {
		next.Macros = *raw.Macros
	} else if raw.MacroEnabled != nil {
		next.Macros = *raw.MacroEnabled
	}

	*o = next
	return nil
}

func (o *FeatureList) UnmarshalJSON(data []byte) error {
	next := *o
	var raw struct {
		EmailSubscribe               *bool `json:"email_subscribe"`
		FriendlyCommentEditorEnabled *bool `json:"friendly_comment_editor_enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.EmailSubscribe != nil {
		next.EmailSubscribe = *raw.EmailSubscribe
	} else if raw.FriendlyCommentEditorEnabled != nil {
		next.EmailSubscribe = *raw.FriendlyCommentEditorEnabled
	}

	*o = next
	return nil
}

func (a *AIModelAssignment) UnmarshalJSON(data []byte) error {
	var raw struct {
		ProviderID      string `json:"provider_id"`
		ProviderIDCamel string `json:"providerId"`
		Model           string `json:"model"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	a.ProviderID = strings.TrimSpace(raw.ProviderID)
	if a.ProviderID == "" {
		a.ProviderID = strings.TrimSpace(raw.ProviderIDCamel)
	}
	a.Model = strings.TrimSpace(raw.Model)
	return nil
}

func (a *AIConfig) UnmarshalJSON(data []byte) error {
	next := *a
	var raw struct {
		Providers                 []AIProvider    `json:"providers"`
		SummaryModel              json.RawMessage `json:"summary_model"`
		CommentReviewModel        json.RawMessage `json:"comment_review_model"`
		EnableSummary             *bool           `json:"enable_summary"`
		EnableAutoGenerateSummary *bool           `json:"enable_auto_generate_summary"`
		AISummaryTargetLanguage   *string         `json:"ai_summary_target_language"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Providers != nil {
		next.Providers = raw.Providers
	}
	if raw.EnableSummary != nil {
		next.EnableSummary = *raw.EnableSummary
	}
	if raw.EnableAutoGenerateSummary != nil {
		next.EnableAutoGenerateSummary = *raw.EnableAutoGenerateSummary
	}
	if raw.AISummaryTargetLanguage != nil {
		next.AISummaryTargetLanguage = *raw.AISummaryTargetLanguage
	}

	var err error
	if len(raw.SummaryModel) > 0 {
		next.SummaryModel, err = parseAIModelAssignment(raw.SummaryModel, next.SummaryModel)
		if err != nil {
			return err
		}
	}
	if len(raw.CommentReviewModel) > 0 {
		next.CommentReviewModel, err = parseAIModelAssignment(raw.CommentReviewModel, next.CommentReviewModel)
		if err != nil {
			return err
		}
	}

	*a = next
	return nil
}

func parseAIModelAssignment(raw json.RawMessage, fallback *AIModelAssignment) (*AIModelAssignment, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return fallback, nil
	}
	if trimmed == "null" {
		return nil, nil
	}

	var legacyModel string
	if err := json.Unmarshal(raw, &legacyModel); err == nil {
		legacyModel = strings.TrimSpace(legacyModel)
		if legacyModel == "" {
			return nil, nil
		}
		next := &AIModelAssignment{}
		if fallback != nil {
			*next = *fallback
		}
		next.Model = legacyModel
		return next, nil
	}

	next := &AIModelAssignment{}
	if fallback != nil {
		*next = *fallback
	}
	if err := json.Unmarshal(raw, next); err != nil {
		return nil, err
	}
	if strings.TrimSpace(next.ProviderID) == "" && strings.TrimSpace(next.Model) == "" {
		return nil, nil
	}
	return next, nil
}

func (p *OAuthProvider) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Type = strings.TrimSpace(raw.Type)
	if p.Type == "" {
		p.Type = strings.TrimSpace(raw.ID)
	}
	p.Enabled = raw.Enabled
	return nil
}

func (p OAuthProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}{
		Type:    strings.TrimSpace(p.Type),
		Enabled: p.Enabled,
	})
}
