package ai

// SummaryPayload is the task payload for summary generation.
type SummaryPayload struct {
	RefID   string `json:"ref_id"`
	RefType string `json:"ref_type"` // post | note | page
	Title   string `json:"title"`
	Lang    string `json:"lang"`
}

type generateSummaryDTO struct {
	RefID string `json:"refId"    binding:"required"`
	Lang  string `json:"lang"`
}

type createSummaryTaskDTO struct {
	RefID       string `json:"refId"`
	RefIDLegacy string `json:"ref_id"`
	Lang        string `json:"lang"`
}

type updateSummaryDTO struct {
	Summary string `json:"summary" binding:"required"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created int64  `json:"created,omitempty"`
}

type providerModelsResponse struct {
	ProviderID   string      `json:"providerId"`
	ProviderName string      `json:"providerName"`
	ProviderType string      `json:"providerType"`
	Models       []modelInfo `json:"models"`
	Error        string      `json:"error,omitempty"`
}

type fetchModelsDTO struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
	APIKey     string `json:"apiKey"`
	Endpoint   string `json:"endpoint"`
}

type testConnectionDTO struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
	APIKey     string `json:"apiKey"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
}
