package models

import "time"

// AnalyzeModel tracks page view analytics.
type AnalyzeModel struct {
	Base
	IP        string                 `json:"ip"        gorm:"index"`
	UA        map[string]interface{} `json:"ua"        gorm:"serializer:json;type:longtext"`
	Country   string                 `json:"country"`
	Path      string                 `json:"path"      gorm:"index"`
	Referer   string                 `json:"referer"   gorm:"index"`
	Timestamp time.Time              `json:"timestamp" gorm:"index;index:idx_ts_path,composite:1;index:idx_ts_ref,composite:1;index:idx_ts_ip,composite:1"`
}

func (AnalyzeModel) TableName() string { return "analyzes" }
