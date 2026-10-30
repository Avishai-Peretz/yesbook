package operations_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/operations"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 2, 15, 4, 5, 123456789, time.FixedZone("IDT", 2*60*60))
	encoded, err := (operations.Cursor{CreatedAt: at, ID: "job-10"}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := operations.ParseCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "job-10" || !parsed.CreatedAt.Equal(at) || parsed.CreatedAt.Location() != time.UTC {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseCursorRejectsUntrustedInput(t *testing.T) {
	t.Parallel()
	valid, err := (operations.Cursor{
		CreatedAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
		ID:        "job-1",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(valid)
	if err != nil {
		t.Fatal(err)
	}
	withTenant := strings.Replace(string(raw), "}", `,"tenant_id":"other"}`, 1)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "not base64", raw: "not base64"},
		{name: "missing id", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-11-02T12:00:00Z"}`))},
		{name: "zero time", raw: base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"0001-01-01T00:00:00Z","id":"job-1"}`))},
		{name: "unknown field", raw: base64.RawURLEncoding.EncodeToString([]byte(withTenant))},
		{name: "trailing value", raw: base64.RawURLEncoding.EncodeToString([]byte(string(raw) + string(raw)))},
		{name: "too long", raw: strings.Repeat("a", 2049)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := operations.ParseCursor(tt.raw); !errors.Is(err, document.ErrInvalid) {
				t.Fatalf("ParseCursor() err = %v", err)
			}
		})
	}
}
