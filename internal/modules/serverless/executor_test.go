package serverless

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestParseRuntimeErrorValue_UsesJSErrorMessage(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString("throw new TypeError('cachedProcessInfo is undefined')")
	if err == nil {
		t.Fatal("expected JS exception")
	}

	exception, ok := err.(*goja.Exception)
	if !ok {
		t.Fatalf("expected *goja.Exception, got %T", err)
	}

	message, status := parseRuntimeErrorValue(exception.Value())
	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
	if !strings.Contains(message, "cachedProcessInfo is undefined") {
		t.Fatalf("expected JS error message, got %q", message)
	}
}
