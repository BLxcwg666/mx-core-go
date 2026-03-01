package webhook

import "time"

// CreateWebhookDTO is the request body for creating a webhook.
type CreateWebhookDTO struct {
	PayloadURL string   `json:"payloadUrl" binding:"required,url"`
	Events     []string `json:"events"      binding:"required,min=1"`
	Enabled    *bool    `json:"enabled"`
	Secret     string   `json:"secret"`
	Scope      *int     `json:"scope"`
}

// UpdateWebhookDTO is the request body for updating a webhook.
type UpdateWebhookDTO struct {
	PayloadURL *string  `json:"payloadUrl"`
	Events     []string `json:"events"`
	Enabled    *bool    `json:"enabled"`
	Secret     *string  `json:"secret"`
	Scope      *int     `json:"scope"`
}

// webhookResponse is the outbound representation of a webhook (no secret).
type webhookResponse struct {
	ID         string    `json:"id"`
	PayloadURL string    `json:"payloadUrl"`
	Events     []string  `json:"events"`
	Enabled    bool      `json:"enabled"`
	Scope      int       `json:"scope"`
	Created    time.Time `json:"created"`
	Modified   time.Time `json:"modified"`
}

// webhookEventEnum is the canonical list of supported event names.
var webhookEventEnum = []string{
	"GATEWAY_CONNECT",
	"GATEWAY_DISCONNECT",
	"VISITOR_ONLINE",
	"VISITOR_OFFLINE",
	"AUTH_FAILED",
	"COMMENT_CREATE",
	"COMMENT_DELETE",
	"COMMENT_UPDATE",
	"POST_CREATE",
	"POST_UPDATE",
	"POST_DELETE",
	"NOTE_CREATE",
	"NOTE_UPDATE",
	"NOTE_DELETE",
	"PAGE_CREATE",
	"PAGE_UPDATE",
	"PAGE_DELETE",
	"TOPIC_CREATE",
	"TOPIC_UPDATE",
	"TOPIC_DELETE",
	"CATEGORY_CREATE",
	"CATEGORY_UPDATE",
	"CATEGORY_DELETE",
	"SAY_CREATE",
	"SAY_DELETE",
	"SAY_UPDATE",
	"LINK_APPLY",
	"RECENTLY_CREATE",
	"RECENTLY_UPDATE",
	"RECENTLY_DELETE",
	"TRANSLATION_CREATE",
	"TRANSLATION_UPDATE",
	"CONTENT_REFRESH",
	"IMAGE_REFRESH",
	"IMAGE_FETCH",
	"ADMIN_NOTIFICATION",
	"STDOUT",
	"ACTIVITY_LIKE",
	"ACTIVITY_UPDATE_PRESENCE",
	"ACTIVITY_LEAVE_PRESENCE",
	"ARTICLE_READ_COUNT_UPDATE",
}

// acceptedWebhookEvents is a set built from webhookEventEnum for O(1) lookup.
var acceptedWebhookEvents = func() map[string]struct{} {
	out := make(map[string]struct{}, len(webhookEventEnum))
	for _, event := range webhookEventEnum {
		out[event] = struct{}{}
	}
	return out
}()
