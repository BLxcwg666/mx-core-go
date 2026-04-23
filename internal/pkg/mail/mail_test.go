package mail

import (
	"testing"

	"github.com/mx-space/core/internal/config"
)

func TestFormatAddressHeaderUsesConfiguredName(t *testing.T) {
	got := formatAddressHeader("noreply@example.com", "xcnya.cn")
	want := "\"xcnya.cn\" <noreply@example.com>"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFormatAddressHeaderPreservesExistingName(t *testing.T) {
	got := formatAddressHeader("\"MX Space\" <noreply@example.com>", "xcnya.cn")
	want := "\"MX Space\" <noreply@example.com>"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSenderResolveFromHeaderFallsBackToUser(t *testing.T) {
	sender := New(Config{User: "noreply@example.com", Name: "xcnya.cn"})
	got := sender.resolveFromHeader()
	want := "\"xcnya.cn\" <noreply@example.com>"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildMailConfigFallsBackToDefaultName(t *testing.T) {
	mc := BuildMailConfig(&config.FullConfig{})
	if mc.Name != "Mx Space" {
		t.Fatalf("expected fallback name %q, got %q", "Mx Space", mc.Name)
	}
}
