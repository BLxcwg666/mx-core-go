package configs

import (
	"encoding/json"
	"testing"
)

func TestMergeOAuthProvidersKeepsOtherProviders(t *testing.T) {
	var existing, incoming interface{}
	_ = json.Unmarshal([]byte(`{"providers":[{"type":"github","enabled":true}]}`), &existing)
	_ = json.Unmarshal([]byte(`{"providers":[{"type":"google","enabled":true}]}`), &incoming)

	out := mergeOAuthProviders(existing, incoming).(map[string]interface{})
	providers := out["providers"].([]interface{})
	if len(providers) != 2 {
		t.Fatalf("want 2 providers, got %v", providers)
	}

	_ = json.Unmarshal([]byte(`{"providers":[{"type":"github","enabled":false}]}`), &incoming)
	out = mergeOAuthProviders(out, incoming).(map[string]interface{})
	providers = out["providers"].([]interface{})
	if len(providers) != 2 || providers[0].(map[string]interface{})["enabled"] != false {
		t.Fatalf("github should be updated in place, got %v", providers)
	}
}
