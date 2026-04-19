package note

import (
	"errors"
	"testing"

	"github.com/mx-space/core/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func TestEnsureNoteAccess(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}

	note := &models.NoteModel{Password: string(hash)}

	tests := []struct {
		name     string
		password string
		isAdmin  bool
		wantErr  error
	}{
		{name: "admin bypasses password", isAdmin: true},
		{name: "missing password rejected", wantErr: errNotePasswordRequired},
		{name: "wrong password rejected", password: "wrong", wantErr: errNotePasswordMismatch},
		{name: "matching password allowed", password: "secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ensureNoteAccess(note, tt.password, tt.isAdmin)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ensureNoteAccess() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEnsureNoteAccessWithoutPassword(t *testing.T) {
	note := &models.NoteModel{}
	if err := ensureNoteAccess(note, "", false); err != nil {
		t.Fatalf("ensureNoteAccess() error = %v, want nil", err)
	}
}
