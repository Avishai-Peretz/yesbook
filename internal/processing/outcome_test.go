package processing

import (
	"context"
	"strconv"
	"testing"

	"github.com/avi-p/yesbook/internal/document"
)

func TestDecideOutcome(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		accepted int
		review   int
		missing  int
		coverage float64
		min      float64
		status   document.Status
		reason   string
	}{
		{name: "no pages", status: document.StatusBlocked, reason: "document has no pages"},
		{name: "all accepted", total: 4, accepted: 4, coverage: 1, min: 0.5, status: document.StatusReady},
		{name: "review wins over coverage", total: 4, accepted: 3, review: 1, coverage: 0.75, min: 0.5, status: document.StatusNeedsReview, reason: "1 of 4 pages flagged for review"},
		{name: "coverage at the threshold", total: 4, accepted: 2, missing: 2, coverage: 0.5, min: 0.5, status: document.StatusPartial, reason: "2 of 4 pages could not be extracted"},
		{name: "coverage below the threshold", total: 4, accepted: 1, missing: 3, coverage: 0.25, min: 0.5, status: document.StatusBlocked, reason: "only 1 of 4 pages passed validation"},
		{name: "mixed review and missing", total: 4, accepted: 1, review: 1, missing: 2, coverage: 0.25, min: 0.5, status: document.StatusNeedsReview, reason: "1 of 4 pages flagged for review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reason := decideOutcome(tt.total, tt.accepted, tt.review, tt.missing, tt.coverage, tt.min)
			if status != tt.status || reason != tt.reason {
				t.Fatalf("got %s %q, want %s %q", status, reason, tt.status, tt.reason)
			}
		})
	}
}

func BenchmarkAssemble(b *testing.B) {
	results := make([]targetResult, 100)
	for i := range results {
		results[i] = targetResult{
			target: document.Target{ID: strconv.Itoa(i), Pages: []int{i + 1}},
			state:  targetAccepted,
			extraction: &document.Extraction{Blocks: []document.Block{{
				Kind: document.BlockParagraph, Page: i + 1, Text: "Photosynthesis converts light into energy.",
			}}},
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		assemble(results, 0.5)
	}
}

type benchExtractor struct{ name string }

func (e benchExtractor) Name() string    { return e.name }
func (e benchExtractor) Version() string { return "v" }
func (e benchExtractor) Extract(context.Context, ExtractRequest) (document.Extraction, error) {
	return document.Extraction{}, nil
}

func BenchmarkRouterNext(b *testing.B) {
	router := NewRouter(map[document.ContentKind][]Extractor{
		document.ContentText: {benchExtractor{"pdfium"}, benchExtractor{"docling"}, benchExtractor{"gemini"}},
	})
	target := document.Target{Content: document.ContentText}
	tried := []string{"pdfium"}
	b.ReportAllocs()
	for b.Loop() {
		router.Next(target, tried, document.ModeAutomatic)
	}
}
