package note

import (
	"testing"

	"github.com/mx-space/core/internal/models"
)

func TestToResponseHidesProtectedTextWithoutAccess(t *testing.T) {
	note := &models.NoteModel{WriteBase: models.WriteBase{Text: "secret text"}, Password: "hashed"}

	resp := toResponse(note, false)

	if resp.Text != "" {
		t.Fatalf("toResponse() text = %q, want empty", resp.Text)
	}
	if !resp.HasPassword {
		t.Fatal("toResponse() HasPassword = false, want true")
	}
}

func TestToResponseKeepsProtectedTextWithAccess(t *testing.T) {
	note := &models.NoteModel{WriteBase: models.WriteBase{Text: "secret text"}, Password: "hashed"}

	resp := toResponse(note, true)

	if resp.Text != "secret text" {
		t.Fatalf("toResponse() text = %q, want secret text", resp.Text)
	}

}
