package processing

import (
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/document"
)

func TestMergeTablesAcrossPages(t *testing.T) {
	header := []string{"name", "grade"}
	blocks := []document.Block{
		{Kind: document.BlockTable, Page: 1, Rows: [][]string{header, {"dana", "90"}}},
		{Kind: document.BlockTable, Page: 2, Rows: [][]string{header, {"yossi", "85"}}},
		{Kind: document.BlockTable, Page: 3, Rows: [][]string{{"a", "b", "c"}}},
	}
	got := mergeTables(blocks)
	if len(got) != 2 {
		t.Fatalf("blocks = %d, want 2", len(got))
	}
	if len(got[0].Rows) != 3 || got[0].EndPage != 2 {
		t.Errorf("merged table = %d rows ending on page %d, want 3 rows ending on page 2", len(got[0].Rows), got[0].EndPage)
	}
}

func TestHebrewDirection(t *testing.T) {
	logical := "שלום לכם חברים יקרים, אנחנו לומדים היום על מים וארץ"
	visual := reverseWords(logical)
	if score, ok := hebrewOrderScore(logical); !ok || score < 0.5 {
		t.Errorf("logical order score = %v (ok=%v), want >= 0.5", score, ok)
	}
	if score, ok := hebrewOrderScore(visual); !ok || score >= 0.5 {
		t.Errorf("visual order score = %v (ok=%v), want < 0.5", score, ok)
	}
	if a := agreement(logical, visual); a < 0.99 {
		t.Errorf("agreement between logical and visual order = %v, want 1", a)
	}
}

func TestValidateHebrewReversedAsksForRetry(t *testing.T) {
	logical := "שלום לכם חברים יקרים, אנחנו לומדים היום על מים וארץ"
	target := document.Target{ID: "p0001", Pages: []int{1}, Language: "he"}
	ex := document.Extraction{Blocks: []document.Block{{Kind: document.BlockParagraph, Page: 1, Text: reverseWords(logical)}}}

	q := DefaultValidator().Validate(target, ex, true)
	if q.Verdict != document.VerdictRetry {
		t.Errorf("verdict = %s (%s), want retry", q.Verdict, q.Reason)
	}
}

func TestValidateMissingLayoutAsksForRetry(t *testing.T) {
	target := document.Target{ID: "p0001", Pages: []int{1}, TextLayer: "Hello world lesson", TextLayerChars: 17}
	ex := document.Extraction{
		Extractor: "pdfium",
		Blocks:    []document.Block{{Kind: document.BlockParagraph, Page: 1, Text: "Hello world lesson"}},
	}
	q := DefaultValidator().Validate(target, ex, true)
	if q.Verdict != document.VerdictRetry {
		t.Fatalf("verdict = %s (%s), want retry", q.Verdict, q.Reason)
	}
	found := false
	for _, c := range q.Checks {
		if c.Name == "layout" && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Errorf("checks = %+v, want failed layout", q.Checks)
	}
}

func TestValidateLayoutAcceptsPositionedBlocks(t *testing.T) {
	target := document.Target{ID: "p0001", Pages: []int{1}, TextLayer: "Hello world", TextLayerChars: 11}
	ex := document.Extraction{
		Extractor: "pdfium",
		Blocks: []document.Block{{
			Kind: document.BlockParagraph,
			Page: 1,
			Text: "Hello world",
			BBox: &document.BBox{Left: 72, Top: 720, Right: 200, Bottom: 700},
		}},
	}
	q := DefaultValidator().Validate(target, ex, true)
	if q.Verdict != document.VerdictAccept {
		t.Fatalf("verdict = %s (%s), want accept", q.Verdict, q.Reason)
	}
}

func TestValidateLayoutSkippedForVisionExtractors(t *testing.T) {
	target := document.Target{ID: "p0001", Pages: []int{1}}
	ex := document.Extraction{
		Extractor: "gemini",
		Blocks:    []document.Block{{Kind: document.BlockParagraph, Page: 1, Text: "Handwritten note"}},
	}
	q := DefaultValidator().Validate(target, ex, false)
	if q.Verdict != document.VerdictAccept {
		t.Fatalf("verdict = %s (%s), want accept without bbox", q.Verdict, q.Reason)
	}
}

func TestValidateMissingFiguresAsksForRetry(t *testing.T) {
	target := document.Target{
		ID: "p0001", Pages: []int{1},
		TextLayer: "Hello world lesson", TextLayerChars: 17, EmbeddedImages: 1,
	}
	ex := document.Extraction{
		Extractor: "pdfium",
		Blocks: []document.Block{{
			Kind: document.BlockParagraph, Page: 1, Text: "Hello world lesson",
			BBox: &document.BBox{Left: 72, Top: 720, Right: 200, Bottom: 700},
		}},
	}
	q := DefaultValidator().Validate(target, ex, true)
	if q.Verdict != document.VerdictRetry {
		t.Fatalf("verdict = %s (%s), want retry", q.Verdict, q.Reason)
	}
	found := false
	for _, c := range q.Checks {
		if c.Name == "figures" && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Errorf("checks = %+v, want failed figures", q.Checks)
	}
}

func TestValidateMissingFiguresReviewsWithoutRetry(t *testing.T) {
	target := document.Target{
		ID: "p0001", Pages: []int{1},
		TextLayer: "Hello world lesson", TextLayerChars: 17, EmbeddedImages: 2,
	}
	ex := document.Extraction{
		Extractor: "pdfium",
		Blocks: []document.Block{{
			Kind: document.BlockParagraph, Page: 1, Text: "Hello world lesson",
			BBox: &document.BBox{Left: 72, Top: 720, Right: 200, Bottom: 700},
		}},
	}
	q := DefaultValidator().Validate(target, ex, false)
	if q.Verdict != document.VerdictReview {
		t.Fatalf("verdict = %s (%s), want review", q.Verdict, q.Reason)
	}
}

func TestValidateFiguresAcceptsCaptionBlocks(t *testing.T) {
	target := document.Target{
		ID: "p0001", Pages: []int{1}, EmbeddedImages: 1,
	}
	ex := document.Extraction{
		Extractor: "gemini",
		Blocks: []document.Block{
			{Kind: document.BlockParagraph, Page: 1, Text: "Hello world"},
			{Kind: document.BlockFigure, Page: 1, Text: "Portrait photo of a person"},
		},
	}
	q := DefaultValidator().Validate(target, ex, true)
	if q.Verdict != document.VerdictAccept {
		t.Fatalf("verdict = %s (%s), want accept", q.Verdict, q.Reason)
	}
	for _, c := range q.Checks {
		if c.Name == "figures" && !strings.Contains(c.Detail, "0 with assets") {
			t.Errorf("figures detail = %q, want caption-only asset count", c.Detail)
		}
	}
}

func TestBuildTargetsRecordsEmbeddedImages(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog near the river bank"
	targets := buildTargets(PDFInfo{Pages: []PageInfo{
		{Number: 1, Text: text, Images: 2},
		{Number: 2, Images: 1},
		{Number: 3},
	}})
	if len(targets) != 3 {
		t.Fatalf("targets = %d, want 3", len(targets))
	}
	if targets[0].Content != document.ContentText || targets[0].EmbeddedImages != 2 {
		t.Errorf("text target = %+v, want ContentText with 2 embedded images", targets[0])
	}
	if targets[1].Content != document.ContentScan || targets[1].EmbeddedImages != 1 {
		t.Errorf("scan target = %+v, want ContentScan with 1 embedded image", targets[1])
	}
	if targets[2].Content != document.ContentBlank || targets[2].EmbeddedImages != 0 {
		t.Errorf("blank target = %+v, want ContentBlank with 0 embedded images", targets[2])
	}
}

func reverseWords(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
