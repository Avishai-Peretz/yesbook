package processing

import (
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

func reverseWords(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
