package models

// AISummaryModel caches AI-generated summaries.
type AISummaryModel struct {
	Base
	Hash    string `json:"hash"    gorm:"uniqueIndex;not null"` // hash(refId + lang)
	Summary string `json:"summary" gorm:"type:text;not null"`
	RefID   string `json:"ref_id"  gorm:"index;not null"`
	Lang    string `json:"lang"    gorm:"default:'default'"`
}

func (AISummaryModel) TableName() string { return "ai_summaries" }

// AIDeepReadingModel stores AI-generated deep reading analysis.
type AIDeepReadingModel struct {
	Base
	Hash             string      `json:"hash"              gorm:"uniqueIndex;not null"`
	RefID            string      `json:"ref_id"            gorm:"index;not null"`
	KeyPoints        StringSlice `json:"key_points"        gorm:"type:json;serializer:json"`
	CriticalAnalysis string      `json:"critical_analysis" gorm:"type:text"`
	Content          string      `json:"content"           gorm:"type:text;not null"`
}

func (AIDeepReadingModel) TableName() string { return "ai_deep_readings" }
