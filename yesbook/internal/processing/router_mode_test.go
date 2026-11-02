package processing_test

import (
	"context"
	"testing"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

type namedExtractor struct{ name string }

func (e namedExtractor) Name() string    { return e.name }
func (e namedExtractor) Version() string { return "v" }
func (e namedExtractor) Extract(context.Context, processing.ExtractRequest) (document.Extraction, error) {
	return document.Extraction{}, nil
}

func TestRouterPinThenEscalate(t *testing.T) {
	router := processing.NewRouter(map[document.ContentKind][]processing.Extractor{
		document.ContentText: {
			namedExtractor{"pdfium"},
			namedExtractor{"docling"},
			namedExtractor{"gemini"},
		},
	})
	target := document.Target{Content: document.ContentText}

	first, ok := router.Next(target, nil, document.ModeGemini)
	if !ok || first.Name() != "gemini" {
		t.Fatalf("pinned first = %v ok=%v", first, ok)
	}
	second, ok := router.Next(target, []string{"gemini"}, document.ModeGemini)
	if !ok || second.Name() != "pdfium" {
		t.Fatalf("escalate = %v ok=%v", second, ok)
	}
	third, ok := router.Next(target, []string{"gemini", "pdfium"}, document.ModeGemini)
	if !ok || third.Name() != "docling" {
		t.Fatalf("escalate docling = %v ok=%v", third, ok)
	}
}

func TestMustTryDocling(t *testing.T) {
	target := document.Target{EmbeddedImages: 2}
	if !processing.MustTryDocling(document.ModeAutomatic, target, "pdfium", []string{"pdfium"}, true) {
		t.Fatal("expected Docling-required for automatic+images")
	}
	if processing.MustTryDocling(document.ModeAutomatic, target, "pdfium", []string{"pdfium", "docling"}, true) {
		t.Fatal("Docling already tried")
	}
	if processing.MustTryDocling(document.ModePDFium, target, "pdfium", []string{"pdfium"}, true) {
		t.Fatal("pinned mode should not force Docling")
	}
	if processing.MustTryDocling(document.ModeAutomatic, target, "pdfium", []string{"pdfium"}, false) {
		t.Fatal("unset Docling should not force retry gate")
	}
}
