package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	anthropicclient "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	appcfg "github.com/mx-space/core/internal/config"
	openaiclient "github.com/openai/openai-go/v2"
	openaioption "github.com/openai/openai-go/v2/option"
	jetai "go.jetify.com/ai"
	jetapi "go.jetify.com/ai/api"
	jetanthropic "go.jetify.com/ai/provider/anthropic"
	jetopenai "go.jetify.com/ai/provider/openai"
)

func isOpenAICompatibleProviderType(raw string) bool {
	t := normalizeProviderType(raw)
	return t == "openai-compatible" || t == "openaicompatible"
}

func isAnthropicProviderType(raw string) bool {
	return normalizeProviderType(raw) == "anthropic"
}

func isOpenRouterProviderType(raw string) bool {
	return normalizeProviderType(raw) == "openrouter"
}

func normalizeProviderType(raw string) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.ReplaceAll(t, "_", "-")
	t = strings.ReplaceAll(t, " ", "")
	return t
}

// callAI calls the AI provider to generate a summary.
func callAI(provider *appcfg.AIProvider, title, text, lang string) (string, error) {
	_ = title
	systemPrompt, prompt := buildSummaryPrompt(lang, text)
	raw, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
	if err != nil {
		return "", err
	}
	return extractSummaryFromAIResponse(raw)
}

func callAIWithPrompt(provider *appcfg.AIProvider, prompt string) (string, error) {
	return callAIWithSystemPrompt(provider, "", prompt)
}

func callAIWithSystemPrompt(provider *appcfg.AIProvider, systemPrompt, prompt string) (string, error) {
	if isOpenAICompatibleProviderType(provider.Type) {
		return callOpenAICompatibleChatCompletions(provider, systemPrompt, prompt)
	}

	model, _, err := buildLanguageModel(provider)
	if err != nil {
		return "", err
	}
	resp, err := jetai.GenerateText(
		context.Background(),
		buildAIPromptMessages(systemPrompt, prompt),
		jetai.WithModel(model),
		jetai.WithMaxOutputTokens(300),
	)
	if err != nil {
		return "", err
	}
	return extractTextFromAIResponse(resp)
}

// callAIStream calls AI with streaming and invokes onToken for each chunk.
func callAIStream(provider *appcfg.AIProvider, title, text, lang string, onToken func(string)) (string, error) {
	_ = title
	systemPrompt, prompt := buildSummaryStreamPrompt(lang, text)

	if isOpenAICompatibleProviderType(provider.Type) {
		return callOpenAICompatibleChatCompletionsStream(provider, systemPrompt, prompt, onToken)
	}

	model, streamEnabled, err := buildLanguageModel(provider)
	if err != nil {
		return "", err
	}

	if !streamEnabled {
		result, err := callAIWithSystemPrompt(provider, systemPrompt, prompt)
		if err != nil {
			return "", err
		}
		if onToken != nil && result != "" {
			onToken(result)
		}
		return result, nil
	}

	streamResp, err := jetai.StreamText(
		context.Background(),
		buildAIPromptMessages(systemPrompt, prompt),
		jetai.WithModel(model),
		jetai.WithMaxOutputTokens(300),
	)
	if err != nil {
		return "", err
	}
	var full strings.Builder
	for event := range streamResp.Stream {
		switch evt := event.(type) {
		case *jetapi.TextDeltaEvent:
			if evt.TextDelta == "" {
				continue
			}
			full.WriteString(evt.TextDelta)
			if onToken != nil {
				onToken(evt.TextDelta)
			}
		case *jetapi.ErrorEvent:
			if evt.Err == nil {
				return "", errors.New("AI stream returned an unknown error")
			}
			return "", fmt.Errorf("%v", evt.Err)
		}
	}
	result := full.String()
	if strings.TrimSpace(result) == "" {
		return "", errors.New("empty response from AI")
	}
	return result, nil
}

func callOpenAICompatibleChatCompletions(provider *appcfg.AIProvider, systemPrompt, prompt string) (string, error) {
	if provider == nil {
		return "", errors.New("AI provider is nil")
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		return "", errors.New("AI provider api key is empty")
	}

	endpoint := normalizeOpenAICompatibleEndpoint(provider.Endpoint)
	model := strings.TrimSpace(provider.DefaultModel)
	if model == "" {
		model = "gpt-4o-mini"
	}

	messages := make([]map[string]string, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": prompt,
	})

	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   messages,
		"max_tokens": 300,
	})

	req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(provider.APIKey))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("openai-compatible error: %s", strings.TrimSpace(string(respBody)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
		return "", fmt.Errorf("openai-compatible error: %s", result.Error.Message)
	}
	if strings.TrimSpace(result.Message) != "" && len(result.Choices) == 0 {
		return "", fmt.Errorf("openai-compatible error: %s", result.Message)
	}
	if len(result.Choices) == 0 {
		return "", errors.New("empty response from AI")
	}
	return result.Choices[0].Message.Content, nil
}

func callOpenAICompatibleChatCompletionsStream(provider *appcfg.AIProvider, systemPrompt, prompt string, onToken func(string)) (string, error) {
	if provider == nil {
		return "", errors.New("AI provider is nil")
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		return "", errors.New("AI provider api key is empty")
	}

	endpoint := normalizeOpenAICompatibleEndpoint(provider.Endpoint)
	model := strings.TrimSpace(provider.DefaultModel)
	if model == "" {
		model = "gpt-4o-mini"
	}

	messages := make([]map[string]string, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": prompt,
	})

	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   messages,
		"max_tokens": 300,
		"stream":     true,
	})

	req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(provider.APIKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai-compatible stream error: %s", strings.TrimSpace(string(respBody)))
	}

	var full strings.Builder
	buf := make([]byte, 4096)
	remainder := ""
	done := false

	for !done {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := remainder + string(buf[:n])
			remainder = ""
			lines := splitLines(chunk)
			for i, line := range lines {
				if i == len(lines)-1 && readErr == nil {
					remainder = line
					continue
				}
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "" {
					continue
				}
				if data == "[DONE]" {
					done = true
					break
				}

				var event struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if err2 := json.Unmarshal([]byte(data), &event); err2 != nil {
					continue
				}
				if len(event.Choices) == 0 || event.Choices[0].Delta.Content == "" {
					continue
				}

				token := event.Choices[0].Delta.Content
				full.WriteString(token)
				if onToken != nil {
					onToken(token)
				}
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}

	result := full.String()
	if strings.TrimSpace(result) == "" {
		return "", errors.New("empty response from AI")
	}
	return result, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func unmarshalAIJSON(raw string, out interface{}) error {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```JSON")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), out); err == nil {
		return nil
	}

	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), out); err == nil {
			return nil
		}
	}

	return fmt.Errorf("invalid JSON response from AI")
}

func extractSummaryFromAIResponse(raw string) (string, error) {
	var output struct {
		Summary string `json:"summary"`
	}
	if err := unmarshalAIJSON(raw, &output); err != nil {
		return "", err
	}
	if strings.TrimSpace(output.Summary) == "" {
		return "", fmt.Errorf("summary is empty in AI response")
	}
	return strings.TrimSpace(output.Summary), nil
}

func buildAIPromptMessages(systemPrompt, prompt string) []jetapi.Message {
	messages := make([]jetapi.Message, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, &jetapi.SystemMessage{Content: systemPrompt})
	}
	messages = append(messages, &jetapi.UserMessage{Content: jetapi.ContentFromText(prompt)})
	return messages
}

func extractTextFromAIResponse(resp *jetapi.Response) (string, error) {
	if resp == nil {
		return "", errors.New("empty response from AI")
	}

	var full strings.Builder
	for _, block := range resp.Content {
		textBlock, ok := block.(*jetapi.TextBlock)
		if !ok || textBlock.Text == "" {
			continue
		}
		full.WriteString(textBlock.Text)
	}

	text := full.String()
	if strings.TrimSpace(text) == "" {
		return "", errors.New("empty response from AI")
	}
	return text, nil
}

func buildLanguageModel(provider *appcfg.AIProvider) (jetapi.LanguageModel, bool, error) {
	if provider == nil {
		return nil, false, errors.New("AI provider is nil")
	}

	apiKey := strings.TrimSpace(provider.APIKey)
	if apiKey == "" {
		return nil, false, errors.New("AI provider api key is empty")
	}

	modelID := strings.TrimSpace(provider.DefaultModel)
	providerType := strings.ToLower(strings.TrimSpace(provider.Type))
	endpoint := strings.TrimSpace(provider.Endpoint)

	if providerType == "anthropic" {
		if modelID == "" {
			modelID = "claude-haiku-4-5-20251001"
		}

		opts := []anthropicoption.RequestOption{
			anthropicoption.WithAPIKey(apiKey),
			anthropicoption.WithMaxRetries(0),
		}
		if endpoint != "" {
			opts = append(opts, anthropicoption.WithBaseURL(strings.TrimRight(endpoint, "/")))
		}

		client := anthropicclient.NewClient(opts...)
		model := jetanthropic.NewLanguageModel(modelID, jetanthropic.WithClient(client))
		return model, false, nil
	}

	if modelID == "" {
		modelID = "gpt-4o-mini"
	}

	opts := []openaioption.RequestOption{
		openaioption.WithAPIKey(apiKey),
		openaioption.WithMaxRetries(0),
	}
	if normalized := normalizeOpenAIBaseURL(endpoint); normalized != "" {
		opts = append(opts, openaioption.WithBaseURL(normalized))
	}

	client := openaiclient.NewClient(opts...)
	model := jetopenai.NewLanguageModel(modelID, jetopenai.WithClient(client))
	return model, true, nil
}

func normalizeOpenAIBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return ""
	}
	parsed, err := neturl.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(base, "/")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/v1") {
		if path == "" {
			path = "/v1"
		} else {
			path += "/v1"
		}
	}
	parsed.Path = path
	return strings.TrimRight(parsed.String(), "/")
}

func normalizeOpenAICompatibleEndpoint(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "https://api.openai.com"
	}

	parsed, err := neturl.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		cleaned := strings.TrimRight(base, "/")
		if strings.HasSuffix(cleaned, "/v1") {
			cleaned = strings.TrimSuffix(cleaned, "/v1")
		}
		return cleaned
	}

	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = path
	return strings.TrimRight(parsed.String(), "/")
}

func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

func selectAIProvider(cfg appcfg.AIConfig, assignment *appcfg.AIModelAssignment) *appcfg.AIProvider {
	var providerID string
	var overrideModel string
	if assignment != nil {
		providerID = strings.TrimSpace(assignment.ProviderID)
		overrideModel = strings.TrimSpace(assignment.Model)
	}

	pick := func(provider appcfg.AIProvider) *appcfg.AIProvider {
		selected := provider
		if overrideModel != "" {
			selected.DefaultModel = overrideModel
		}
		return &selected
	}

	if providerID != "" {
		for _, provider := range cfg.Providers {
			if !provider.Enabled {
				continue
			}
			if strings.TrimSpace(provider.ID) != providerID {
				continue
			}
			return pick(provider)
		}
	}

	for _, provider := range cfg.Providers {
		if !provider.Enabled {
			continue
		}
		return pick(provider)
	}

	return nil
}

func modelsFromProvider(provider appcfg.AIProvider) []modelInfo {
	models := make([]modelInfo, 0, 1)
	if provider.DefaultModel != "" {
		models = append(models, modelInfo{
			ID:   provider.DefaultModel,
			Name: provider.DefaultModel,
		})
	}
	return models
}

func fetchModelsFromProvider(provider appcfg.AIProvider) ([]modelInfo, error) {
	switch {
	case isAnthropicProviderType(provider.Type):
		endpoint := normalizeAnthropicModelsEndpoint(provider.Endpoint)
		headers := map[string]string{
			"x-api-key":         strings.TrimSpace(provider.APIKey),
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
			"accept":            "application/json",
		}
		return fetchModelsByEndpoint(endpoint, headers, parseAnthropicModels)
	case isOpenRouterProviderType(provider.Type):
		endpoint := normalizeOpenRouterModelsEndpoint(provider.Endpoint)
		headers := map[string]string{
			"authorization": "Bearer " + strings.TrimSpace(provider.APIKey),
			"accept":        "application/json",
		}
		return fetchModelsByEndpoint(endpoint, headers, parseOpenAIStyleModels)
	default:
		endpoint := normalizeOpenAIModelsEndpoint(provider.Endpoint)
		headers := map[string]string{
			"authorization": "Bearer " + strings.TrimSpace(provider.APIKey),
			"accept":        "application/json",
		}
		return fetchModelsByEndpoint(endpoint, headers, parseOpenAIStyleModels)
	}
}

func fetchModelsByEndpoint(endpoint string, headers map[string]string, parser func([]byte) ([]modelInfo, error)) ([]modelInfo, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("provider models request failed: %s", strings.TrimSpace(string(body)))
	}
	models, err := parser(body)
	if err != nil {
		return nil, err
	}
	return dedupeModelInfos(models), nil
}

func parseOpenAIStyleModels(body []byte) ([]modelInfo, error) {
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	models := make([]modelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = id
		}
		models = append(models, modelInfo{ID: id, Name: name})
	}
	return models, nil
}

func parseAnthropicModels(body []byte) ([]modelInfo, error) {
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	models := make([]modelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = id
		}
		models = append(models, modelInfo{ID: id, Name: name})
	}
	return models, nil
}

func dedupeModelInfos(input []modelInfo) []modelInfo {
	if len(input) == 0 {
		return []modelInfo{}
	}
	out := make([]modelInfo, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = id
		}
		out = append(out, modelInfo{
			ID:   id,
			Name: name,
		})
	}
	return out
}

func normalizeOpenAIModelsEndpoint(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "https://api.openai.com/v1/models"
	}
	parsed, err := neturl.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		cleaned := strings.TrimRight(base, "/")
		cleaned = strings.TrimSuffix(cleaned, "/v1")
		cleaned = strings.TrimSuffix(cleaned, "/models")
		return cleaned + "/v1/models"
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/models")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimRight(path, "/") + "/v1/models"
	return strings.TrimRight(parsed.String(), "/")
}

func normalizeAnthropicModelsEndpoint(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "https://api.anthropic.com/v1/models"
	}
	parsed, err := neturl.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		cleaned := strings.TrimRight(base, "/")
		cleaned = strings.TrimSuffix(cleaned, "/v1")
		cleaned = strings.TrimSuffix(cleaned, "/models")
		return cleaned + "/v1/models"
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/models")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimRight(path, "/") + "/v1/models"
	return strings.TrimRight(parsed.String(), "/")
}

func normalizeOpenRouterModelsEndpoint(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "https://openrouter.ai/api/v1/models"
	}
	parsed, err := neturl.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		cleaned := strings.TrimRight(base, "/")
		cleaned = strings.TrimSuffix(cleaned, "/models")
		cleaned = strings.TrimSuffix(cleaned, "/api/v1")
		cleaned = strings.TrimSuffix(cleaned, "/v1")
		return cleaned + "/api/v1/models"
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/models")
	if strings.HasSuffix(path, "/api/v1") {
		path = strings.TrimSuffix(path, "/api/v1")
	} else if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimRight(path, "/") + "/api/v1/models"
	return strings.TrimRight(parsed.String(), "/")
}

// jsonMarshal is a package-level helper to avoid importing encoding/json in other files
// when they only need to call json.Marshal for event data.
func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
