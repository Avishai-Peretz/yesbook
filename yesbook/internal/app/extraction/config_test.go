package extraction

import (
	"context"
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

type stubExtractor struct{ name string }

func (s stubExtractor) Name() string    { return s.name }
func (s stubExtractor) Version() string { return "v" }
func (s stubExtractor) Extract(context.Context, processing.ExtractRequest) (document.Extraction, error) {
	return document.Extraction{}, nil
}

func TestChainSkipsNil(t *testing.T) {
	got := chain(nil, stubExtractor{name: "pdfium"}, nil)
	if len(got) != 1 || got[0].Name() != "pdfium" {
		t.Fatalf("chain length = %d", len(got))
	}
}

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	cfg, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	def := processing.DefaultConfig()
	if cfg.MaxAttempts != int64(def.Budget.MaxAttempts) || cfg.MaxCostMicros != def.Budget.MaxCostMicros {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Pipeline().Budget.MaxDuration != def.Budget.MaxDuration {
		t.Fatalf("duration = %s", cfg.Pipeline().Budget.MaxDuration)
	}
	_, err = loadConfig(func(key string) string {
		if key == "YESBOOK_MAX_ATTEMPTS" {
			return "-1"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative limit error = %v", err)
	}
	_, err = loadConfig(func(key string) string {
		if key == "YESBOOK_MAX_COST_MICROS" {
			return "nope"
		}
		return ""
	})
	if err == nil {
		t.Fatal("invalid integer should be rejected")
	}
}

func TestConfiguredPriceParsesOnlyProvidedValues(t *testing.T) {
	price, err := configuredPrice("YESBOOK_GEMINI", "", "15")
	if err != nil || price.InputPerMillion != 0 || price.OutputPerMillion != 15 {
		t.Fatalf("price = %+v, err = %v", price, err)
	}
}
