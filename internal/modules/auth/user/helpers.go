package user

import (
	"encoding/json"
	"strings"

	"github.com/mx-space/core/internal/models"
)

func toResponse(u *models.UserModel) *userResponse {
	return &userResponse{
		ID: u.ID, Username: u.Username, Name: u.Name,
		Introduce: u.Introduce, Avatar: u.Avatar, Mail: u.Mail, URL: u.URL,
		SocialIDs:     parseSocialIDs(u.SocialIDs),
		LastLoginTime: u.LastLoginTime, LastLoginIP: u.LastLoginIP,
	}
}

func toPublicResponse(u *models.UserModel) *publicUserResponse {
	return &publicUserResponse{
		ID: u.ID, Username: u.Username, Name: u.Name,
		Introduce: u.Introduce, Avatar: u.Avatar, Mail: u.Mail, URL: u.URL,
		SocialIDs: parseSocialIDs(u.SocialIDs),
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func parseSocialIDs(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

func encodeSocialIDs(ids map[string]interface{}) (string, error) {
	if ids == nil {
		return "{}", nil
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func oauthProviderClientID(public map[string]interface{}, providerType string) string {
	if len(public) == 0 || strings.TrimSpace(providerType) == "" {
		return ""
	}
	raw, ok := public[providerType]
	if !ok {
		for k, v := range public {
			if strings.EqualFold(k, providerType) {
				raw = v
				ok = true
				break
			}
		}
		if !ok {
			return ""
		}
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range []string{"client_id", "clientId"} {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
