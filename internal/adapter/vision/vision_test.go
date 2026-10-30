package vision_test

import (
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/vision"
	"github.com/avi-p/yesbook/internal/document"
)

func TestParse(t *testing.T) {
	raw := `{"blocks": [
		{"kind": "heading", "page": 2, "text": " משוואות ", "rows": []},
		{"kind": "formula", "page": 0, "text": "x^2 + y^2 = r^2", "rows": []},
		{"kind": "paragraph", "page": 2, "text": "  ", "rows": []},
		{"kind": "table", "page": 2, "text": "ignored", "rows": [["a", "b"], ["1", "2"]]}
	]}`
	blocks, err := vision.Parse(raw, document.Target{Pages: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v, want empty paragraph dropped", blocks)
	}
	if blocks[0].Text != "משוואות" || blocks[1].Page != 2 || blocks[2].Text != "" || len(blocks[2].Rows) != 2 {
		t.Errorf("blocks = %+v", blocks)
	}
}

func TestPromptIncludesContext(t *testing.T) {
	p := vision.Prompt(document.Target{Pages: []int{4}, Content: document.ContentHandwriting, Language: "he", Context: "previous page"})
	for _, want := range []string{"page 4", "handwriting", `"he"`, "<context>\nprevious page"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
}

func TestPricing(t *testing.T) {
	p := vision.Pricing{InputPerMillion: 300_000, OutputPerMillion: 2_500_000}
	if got := p.Cost(1_000, 500); got != 1_550 {
		t.Errorf("cost = %d micros, want 1550", got)
	}
}
