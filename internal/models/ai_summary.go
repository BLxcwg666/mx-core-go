package models

import "gorm.io/gorm"

// AISummaryModel caches the AI-generated summary of an article (one per article; the legacy
// "lang" column is left in place but no longer used).
type AISummaryModel struct {
	Base
	Hash    string `json:"hash"    gorm:"uniqueIndex;not null"` // hash(refId)
	Summary string `json:"summary" gorm:"type:text;not null"`
	RefID   string `json:"ref_id"  gorm:"index;not null"`
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

// DeleteAIDataByRef removes the AI summary and deep reading of a deleted article.
func DeleteAIDataByRef(db *gorm.DB, refID string) error {
	if err := db.Where("ref_id = ?", refID).Delete(&AISummaryModel{}).Error; err != nil {
		return err
	}
	return db.Where("ref_id = ?", refID).Delete(&AIDeepReadingModel{}).Error
}
