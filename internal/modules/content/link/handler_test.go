package link

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
)

type webhookDispatch struct {
	event   string
	payload interface{}
	scope   int
}

type webhookRecorder struct {
	dispatches []webhookDispatch
}

func (r *webhookRecorder) DispatchScoped(event string, payload interface{}, scope int) {
	r.dispatches = append(r.dispatches, webhookDispatch{event: event, payload: payload, scope: scope})
}

func TestDispatchContentRefresh(t *testing.T) {
	recorder := &webhookRecorder{}
	h := &Handler{webhook: recorder}

	h.dispatchContentRefresh("link-id")

	if len(recorder.dispatches) != 1 {
		t.Fatalf("dispatch count = %d, want 1", len(recorder.dispatches))
	}
	dispatch := recorder.dispatches[0]
	if dispatch.event != "CONTENT_REFRESH" {
		t.Fatalf("event = %q, want CONTENT_REFRESH", dispatch.event)
	}
	if dispatch.scope != webhook.ScopeToSystem|webhook.ScopeToVisitor {
		t.Fatalf("scope = %d, want system and visitor", dispatch.scope)
	}
	payload, ok := dispatch.payload.(gin.H)
	if !ok {
		t.Fatalf("payload type = %T, want gin.H", dispatch.payload)
	}
	if payload["type"] != "link" || payload["id"] != "link-id" {
		t.Fatalf("payload = %#v, want link refresh payload", payload)
	}
}
