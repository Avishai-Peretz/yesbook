package modes_test

import (
	"errors"
	"testing"

	"github.com/avi-p/yesbook/internal/app/modes"
	"github.com/avi-p/yesbook/internal/document"
)

func TestConfiguredAndValidate(t *testing.T) {
	cfg := modes.Configured(func(key string) string {
		switch key {
		case "YESBOOK_DOCLING_URL":
			return "http://docling"
		case "YESBOOK_OPENAI_API_KEY":
			return "sk-test"
		default:
			return ""
		}
	})
	if !cfg["pdfium"] || !cfg["docling"] || !cfg["openai"] || cfg["gemini"] {
		t.Fatalf("configured = %#v", cfg)
	}
	if err := modes.Validate(document.ModeAutomatic, cfg); err != nil {
		t.Fatal(err)
	}
	if err := modes.Validate(document.ModeDocling, cfg); err != nil {
		t.Fatal(err)
	}
	if err := modes.Validate(document.ModeGemini, cfg); !errors.Is(err, document.ErrInvalid) {
		t.Fatalf("gemini unset err = %v", err)
	}
}
