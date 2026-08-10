package webhook

import (
	"testing"

	"github.com/mx-space/core/internal/models"
)

func TestBuildWebhookCommentPayloadPrivacy(t *testing.T) {
	comment := &models.CommentModel{
		Mail:  "reader@example.com",
		IP:    "127.0.0.1",
		Agent: "test-agent",
	}
	svc := &Service{}

	publicPayload := svc.buildWebhookCommentPayload(comment, false)
	for _, key := range []string{"mail", "ip", "agent"} {
		if _, ok := publicPayload[key]; ok {
			t.Fatalf("public payload contains private field %q", key)
		}
	}

	privatePayload := svc.buildWebhookCommentPayload(comment, true)
	for _, key := range []string{"mail", "ip", "agent"} {
		if _, ok := privatePayload[key]; !ok {
			t.Fatalf("private payload does not contain field %q", key)
		}
	}
}

func TestBuildWebhookContentPayloadPrivacyAndTypes(t *testing.T) {
	postPayload := buildWebhookPostPayload(&models.PostModel{Pin: true})
	if pin, ok := postPayload["pin"].(bool); !ok || !pin {
		t.Fatalf("post pin = %#v, want true", postPayload["pin"])
	}

	notePayload := buildWebhookNotePayload(&models.NoteModel{
		WriteBase: models.WriteBase{Text: "private content"},
		Password:  "password-hash",
	})
	if notePayload["text"] != "" {
		t.Fatalf("password-protected note text = %q, want empty", notePayload["text"])
	}
}
