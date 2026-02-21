package models

import "time"

// WebhookModel defines an outbound webhook endpoint.
type WebhookModel struct {
	Base
	PayloadURL string   `json:"payload_url" gorm:"not null"`
	Events     []string `json:"events"      gorm:"serializer:json"`
	Enabled    bool     `json:"enabled"     gorm:"default:true"`
	Secret     string   `json:"-"           gorm:"not null"`
	Scope      string   `json:"scope"`

	EventLogs []WebhookEventModel `json:"event_logs,omitempty" gorm:"foreignKey:HookID"`
}

func (WebhookModel) TableName() string { return "webhooks" }

// WebhookEventModel is the audit trail of webhook deliveries.
type WebhookEventModel struct {
	Base
	HookID    string                 `json:"hook_id"   gorm:"index;not null"`
	Event     string                 `json:"event"     gorm:"not null"`
	Headers   map[string]interface{} `json:"headers"   gorm:"serializer:json"`
	Payload   map[string]interface{} `json:"payload"   gorm:"serializer:json"`
	Response  map[string]interface{} `json:"response"  gorm:"serializer:json"`
	Success   bool                   `json:"success"`
	Status    int                    `json:"status"`
	Timestamp time.Time              `json:"timestamp" gorm:"index"`
}

func (WebhookEventModel) TableName() string { return "webhook_events" }
