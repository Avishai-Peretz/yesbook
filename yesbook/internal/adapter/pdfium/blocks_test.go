package pdfium

import (
	"strings"
	"testing"

	"github.com/klippa-app/go-pdfium/responses"

	"github.com/avi-p/yesbook/internal/document"
)

func rect(text string, left, top, right, bottom, size float64) *responses.GetPageTextStructuredRect {
	return &responses.GetPageTextStructuredRect{
		Text: text,
		PointPosition: responses.CharPosition{
			Left: left, Top: top, Right: right, Bottom: bottom,
		},
		FontInformation: &responses.FontInformation{Size: size, RenderedSize: size},
	}
}

func TestBlocksFromRectsSplitsParagraphsAndHeadings(t *testing.T) {
	rects := []*responses.GetPageTextStructuredRect{
		rect("Resume Title", 72, 750, 300, 730, 24),
		rect("Experience", 72, 700, 200, 684, 18),
		rect("Built APIs", 72, 670, 250, 656, 12),
		rect("in Go.", 260, 670, 320, 656, 12),
		rect("• Shipped features", 72, 620, 280, 606, 12),
	}
	blocks := blocksFromRects(1, rects)
	if len(blocks) < 3 {
		t.Fatalf("blocks = %d (%+v), want at least heading + body splits", len(blocks), blocks)
	}
	if blocks[0].Kind != document.BlockHeading || blocks[0].Text != "Resume Title" {
		t.Errorf("first block = %+v, want heading Resume Title", blocks[0])
	}
	if blocks[0].BBox == nil || blocks[0].BBox.Top != 750 || blocks[0].Page != 1 {
		t.Errorf("heading bbox/page = %+v", blocks[0])
	}

	foundList := false
	foundBody := false
	for _, b := range blocks {
		if b.BBox == nil {
			t.Errorf("block missing bbox: %+v", b)
		}
		if b.Text == "• Shipped features" {
			foundList = true
		}
		if strings.Contains(b.Text, "Built APIs") && strings.Contains(b.Text, "in Go.") {
			foundBody = true
		}
	}
	if !foundList {
		t.Errorf("list item not preserved in blocks = %+v", blocks)
	}
	if !foundBody {
		t.Errorf("same-line fragments not merged: %+v", blocks)
	}
}

func TestBlocksFromRectsEmpty(t *testing.T) {
	if got := blocksFromRects(1, nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
