package observability

import (
	"errors"
	"os"
	"testing"
)

func TestResolveDSN_disabled(t *testing.T) {
	t.Setenv("FABRIKA_SENTRY_DISABLE", "1")
	if got := ResolveDSN(); got != "" {
		t.Fatalf("expected empty DSN when disabled, got %q", got)
	}
}

func TestResolveDSN_override(t *testing.T) {
	os.Unsetenv("FABRIKA_SENTRY_DISABLE")
	t.Setenv("FABRIKA_SENTRY_DSN", "https://example.ingest.sentry.io/123")
	if got := ResolveDSN(); got != "https://example.ingest.sentry.io/123" {
		t.Fatalf("expected override DSN, got %q", got)
	}
}

func TestResolveDSN_default(t *testing.T) {
	os.Unsetenv("FABRIKA_SENTRY_DISABLE")
	os.Unsetenv("FABRIKA_SENTRY_DSN")
	if got := ResolveDSN(); got != DefaultDSN {
		t.Fatalf("expected DefaultDSN, got %q", got)
	}
}

func TestInit_disabled(t *testing.T) {
	flush, err := Init("", "", "")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if flush == nil {
		t.Fatal("flush func must be non-nil")
	}
	flush() // must not panic
}

func TestCaptureError_nil(t *testing.T) {
	CaptureError(nil) // must not panic
}

func TestCaptureError_noSentry(t *testing.T) {
	CaptureError(errors.New("test error")) // must not panic when sentry not initialized
}
