package configs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/mx-space/core/internal/config"
)

func deepMergeJSON(oldVal, newVal interface{}) interface{} {
	oldMap, oldIsMap := oldVal.(map[string]interface{})
	newMap, newIsMap := newVal.(map[string]interface{})
	if oldIsMap && newIsMap {
		out := make(map[string]interface{}, len(oldMap))
		for k, v := range oldMap {
			out[k] = v
		}
		for k, v := range newMap {
			if existing, ok := out[k]; ok {
				out[k] = deepMergeJSON(existing, v)
				continue
			}
			out[k] = v
		}
		return out
	}

	// Arrays should be replaced as a whole.
	if _, ok := newVal.([]interface{}); ok {
		return newVal
	}

	return newVal
}

func shouldEnableCommentAIReview(partial map[string]json.RawMessage) bool {
	for _, sectionKey := range []string{"comment_options", "commentOptions"} {
		raw, ok := partial[sectionKey]
		if !ok || len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		for _, field := range []string{"ai_review", "aiReview"} {
			enabled, ok := parseBoolFromAny(payload[field])
			if ok && enabled {
				return true
			}
		}
	}
	return false
}

func hasEnabledAIProvider(providers []config.AIProvider) bool {
	for _, provider := range providers {
		if provider.Enabled {
			return true
		}
	}
	return false
}

func parseBoolFromAny(v interface{}) (bool, bool) {
	switch value := v.(type) {
	case bool:
		return value, true
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(value))
		switch trimmed {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	case float64:
		return value != 0, true
	case float32:
		return value != 0, true
	case int:
		return value != 0, true
	case int8:
		return value != 0, true
	case int16:
		return value != 0, true
	case int32:
		return value != 0, true
	case int64:
		return value != 0, true
	case uint:
		return value != 0, true
	case uint8:
		return value != 0, true
	case uint16:
		return value != 0, true
	case uint32:
		return value != 0, true
	case uint64:
		return value != 0, true
	}
	return false, false
}

func normalizeConfigSection(key string, v interface{}) interface{} {
	switch key {
	case "mail_options":
		return normalizeMailOptions(v)
	case "friend_link_options":
		return normalizeFriendLinkOptions(v)
	case "image_bed_options":
		return normalizeImageBedOptions(v)
	case "image_storage_options":
		return normalizeImageStorageOptions(v)
	case "text_options":
		return normalizeTextOptions(v)
	case "feature_list":
		return normalizeFeatureList(v)
	case "oauth":
		return normalizeOAuthConfig(v)
	default:
		return v
	}
}

func normalizeMailOptions(v interface{}) interface{} {
	mailMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}

	smtpRaw, ok := mailMap["smtp"]
	if !ok || smtpRaw == nil {
		return mailMap
	}

	smtpMap, ok := smtpRaw.(map[string]interface{})
	if !ok {
		return mailMap
	}

	optionsMap := map[string]interface{}{}
	if rawOptions, ok := smtpMap["options"]; ok && rawOptions != nil {
		if parsedOptions, ok := rawOptions.(map[string]interface{}); ok {
			for key, value := range parsedOptions {
				optionsMap[key] = value
			}
		}
	}

	if host, ok := smtpMap["host"]; ok {
		optionsMap["host"] = host
	}
	if port, ok := smtpMap["port"]; ok {
		optionsMap["port"] = port
	}
	if secure, ok := smtpMap["secure"]; ok {
		optionsMap["secure"] = secure
	}

	if len(optionsMap) > 0 {
		smtpMap["options"] = optionsMap
	}

	delete(smtpMap, "host")
	delete(smtpMap, "port")
	delete(smtpMap, "secure")

	mailMap["smtp"] = smtpMap
	return mailMap
}

func normalizeFriendLinkOptions(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if _, exists := sectionMap["enable_avatar_internalization"]; !exists {
		if legacy, ok := sectionMap["avatar_internationalization"]; ok {
			sectionMap["enable_avatar_internalization"] = legacy
		}
	}
	delete(sectionMap, "avatar_internationalization")
	return sectionMap
}

func normalizeImageBedOptions(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if _, exists := sectionMap["max_size_mb"]; !exists {
		if legacy, ok := sectionMap["max_size"]; ok {
			sectionMap["max_size_mb"] = legacy
		}
	}
	delete(sectionMap, "max_size")
	return sectionMap
}

func normalizeImageStorageOptions(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if _, exists := sectionMap["delete_local_after_sync"]; !exists {
		if legacy, ok := sectionMap["auto_delete_after_sync"]; ok {
			sectionMap["delete_local_after_sync"] = legacy
		}
	}
	delete(sectionMap, "auto_delete_after_sync")
	return sectionMap
}

func normalizeTextOptions(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if _, exists := sectionMap["macros"]; !exists {
		if legacy, ok := sectionMap["macro_enabled"]; ok {
			sectionMap["macros"] = legacy
		}
	}
	delete(sectionMap, "macro_enabled")
	return sectionMap
}

func normalizeFeatureList(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if _, exists := sectionMap["email_subscribe"]; !exists {
		if legacy, ok := sectionMap["friendly_comment_editor_enabled"]; ok {
			sectionMap["email_subscribe"] = legacy
		}
	}
	delete(sectionMap, "friendly_comment_editor_enabled")
	return sectionMap
}

func normalizeOAuthConfig(v interface{}) interface{} {
	sectionMap, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	providers, ok := sectionMap["providers"].([]interface{})
	if !ok {
		return sectionMap
	}

	for i, providerRaw := range providers {
		providerMap, ok := providerRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, exists := providerMap["type"]; !exists {
			if legacy, ok := providerMap["id"]; ok {
				providerMap["type"] = legacy
			}
		}
		delete(providerMap, "id")
		providers[i] = providerMap
	}
	sectionMap["providers"] = providers
	return sectionMap
}

func loadFormSchemaTemplate() (map[string]interface{}, error) {
	formSchemaLoadOnce.Do(func() {
		decoded := decodeJSONBytes(formSchemaTemplateRaw)
		if err := json.Unmarshal(decoded, &formSchemaTemplate); err != nil {
			formSchemaLoadErr = err
		}
	})
	if formSchemaLoadErr != nil {
		return nil, formSchemaLoadErr
	}

	raw, err := json.Marshal(formSchemaTemplate)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeJSONBytes(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}

	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		return utf16ToUTF8(raw[2:], true)
	}

	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return utf16ToUTF8(raw[2:], false)
	}

	return raw
}

func utf16ToUTF8(raw []byte, littleEndian bool) []byte {
	if len(raw) < 2 {
		return []byte{}
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if littleEndian {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
			continue
		}
		u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return []byte(string(utf16.Decode(u16)))
}

func attachAIProviderOptions(schema map[string]interface{}, aiCfg config.AIConfig) {
	options := make([]providerSelectOption, 0, len(aiCfg.Providers))
	seen := map[string]struct{}{}

	addOption := func(id, label string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(label) == "" {
			label = id
		}
		options = append(options, providerSelectOption{Label: label, Value: id})
	}

	for _, provider := range aiCfg.Providers {
		addOption(provider.ID, formatAIProviderLabel(provider))
	}

	if aiCfg.SummaryModel != nil {
		addOption(aiCfg.SummaryModel.ProviderID, aiCfg.SummaryModel.ProviderID)
	}
	if aiCfg.CommentReviewModel != nil {
		addOption(aiCfg.CommentReviewModel.ProviderID, aiCfg.CommentReviewModel.ProviderID)
	}
	if len(options) == 0 {
		return
	}

	groups, ok := schema["groups"].([]interface{})
	if !ok {
		return
	}

	for _, group := range groups {
		groupMap, ok := group.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.TrimSpace(fmt.Sprintf("%v", groupMap["key"])) != "ai" {
			continue
		}

		sections, ok := groupMap["sections"].([]interface{})
		if !ok {
			continue
		}
		for _, section := range sections {
			sectionMap, ok := section.(map[string]interface{})
			if !ok {
				continue
			}
			if strings.TrimSpace(fmt.Sprintf("%v", sectionMap["key"])) != "ai" {
				continue
			}

			fields, ok := sectionMap["fields"].([]interface{})
			if !ok {
				continue
			}
			attachAIProviderOptionsToFields(fields, options)
		}
	}
}

func attachAIProviderOptionsToFields(fields []interface{}, options []providerSelectOption) {
	for _, field := range fields {
		fieldMap, ok := field.(map[string]interface{})
		if !ok {
			continue
		}

		if strings.TrimSpace(fmt.Sprintf("%v", fieldMap["key"])) == "providerId" &&
			strings.TrimSpace(fmt.Sprintf("%v", fieldMap["title"])) == "Provider ID" {
			ui, _ := fieldMap["ui"].(map[string]interface{})
			if ui == nil {
				ui = map[string]interface{}{}
				fieldMap["ui"] = ui
			}

			ui["component"] = "select"
			selectOptions := make([]map[string]interface{}, 0, len(options))
			for _, option := range options {
				selectOptions = append(selectOptions, map[string]interface{}{
					"label": option.Label,
					"value": option.Value,
				})
			}
			ui["options"] = selectOptions
		}

		nestedFields, ok := fieldMap["fields"].([]interface{})
		if !ok {
			continue
		}
		attachAIProviderOptionsToFields(nestedFields, options)
	}
}

func formatAIProviderLabel(provider config.AIProvider) string {
	name := strings.TrimSpace(provider.Name)
	providerType := strings.TrimSpace(provider.Type)
	id := strings.TrimSpace(provider.ID)

	displayName := name
	if providerNameUUIDPattern.MatchString(displayName) {
		displayName = ""
	}

	if displayName != "" && providerType != "" {
		return fmt.Sprintf("%s (%s)", displayName, providerType)
	}
	if displayName != "" {
		return displayName
	}
	if providerType != "" {
		return providerType
	}
	if id != "" {
		return id
	}
	return "Unknown"
}

var optionKeyAliases = map[string]string{
	"seo":                             "seo",
	"url":                             "url",
	"mail_options":                    "mail_options",
	"comment_options":                 "comment_options",
	"backup_options":                  "backup_options",
	"baidu_search_options":            "baidu_search_options",
	"algolia_search_options":          "algolia_search_options",
	"admin_extra":                     "admin_extra",
	"friend_link_options":             "friend_link_options",
	"s3_options":                      "s3_options",
	"image_bed_options":               "image_bed_options",
	"image_storage_options":           "image_storage_options",
	"third_party_service_integration": "third_party_service_integration",
	"text_options":                    "text_options",
	"bing_search_options":             "bing_search_options",
	"meili_search_options":            "meili_search_options",
	"feature_list":                    "feature_list",
	"bark_options":                    "bark_options",
	"auth_security":                   "auth_security",
	"ai":                              "ai",
	"oauth":                           "oauth",
}

func normalizeOptionKey(key string) string {
	snake := camelToSnakeKey(key)
	if _, ok := optionKeyAliases[snake]; ok {
		return snake
	}
	return snake
}

func normalizeJSONKeys(raw json.RawMessage, keyFn func(string) string) (json.RawMessage, error) {
	var data interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("invalid json body")
	}
	normalized := convertMapKeys(data, keyFn)
	out, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func convertMapKeys(v interface{}, keyFn func(string) string) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			out[keyFn(k)] = convertMapKeys(child, keyFn)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, child := range val {
			out[i] = convertMapKeys(child, keyFn)
		}
		return out
	case *config.FullConfig:
		if val == nil {
			return nil
		}
		b, _ := json.Marshal(val)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		return convertMapKeys(m, keyFn)
	case config.FullConfig:
		b, _ := json.Marshal(val)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		return convertMapKeys(m, keyFn)
	default:
		return val
	}
}

func snakeToCamelKey(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	out := make([]rune, 0, len(s))
	out = append(out, []rune(parts[0])...)
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		switch lower {
		case "mb":
			out = append(out, []rune("MB")...)
			continue
		case "ttl":
			out = append(out, []rune("TTL")...)
			continue
		}
		runes := []rune(lower)
		runes[0] = unicode.ToUpper(runes[0])
		out = append(out, runes...)
	}
	return string(out)
}

func camelToSnakeKey(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) == 0 {
		return ""
	}
	out := make([]rune, 0, len(runes)+4)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
					out = append(out, '_')
				}
			}
			out = append(out, unicode.ToLower(r))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
