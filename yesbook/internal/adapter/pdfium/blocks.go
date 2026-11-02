package pdfium

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/klippa-app/go-pdfium/responses"

	"github.com/avi-p/yesbook/internal/document"
)

// lineGapFactor starts a new paragraph when the vertical gap between lines
// exceeds this multiple of the previous line's height.
const lineGapFactor = 1.4

// headingSizeFactor marks a block as a heading when its font is at least this
// multiple of the page's median font size and the text is short.
const headingSizeFactor = 1.2

const maxHeadingRunes = 120

type span struct {
	text     string
	bbox     document.BBox
	fontSize float64
}

// blocksFromRects turns PDFium text rects into reading-order blocks with
// bounding boxes. Rects on the same line are merged; vertical gaps and font
// size jumps open a new block. Larger-than-median short runs become headings.
func blocksFromRects(page int, rects []*responses.GetPageTextStructuredRect) []document.Block {
	spans := make([]span, 0, len(rects))
	sizes := make([]float64, 0, len(rects))
	for _, r := range rects {
		if r == nil {
			continue
		}
		text := strings.TrimSpace(r.Text)
		if text == "" {
			continue
		}
		s := span{
			text: text,
			bbox: document.BBox{
				Left:   r.PointPosition.Left,
				Top:    r.PointPosition.Top,
				Right:  r.PointPosition.Right,
				Bottom: r.PointPosition.Bottom,
			},
			fontSize: spanFontSize(r),
		}
		spans = append(spans, s)
		if s.fontSize > 0 {
			sizes = append(sizes, s.fontSize)
		}
	}
	if len(spans) == 0 {
		return nil
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if nearlyEqual(spans[i].bbox.Top, spans[j].bbox.Top, 1) {
			return spans[i].bbox.Left < spans[j].bbox.Left
		}
		return spans[i].bbox.Top > spans[j].bbox.Top
	})

	lines := groupLines(spans)
	median := medianFloat(sizes)
	var blocks []document.Block
	var cur []span
	var curSize float64
	flush := func() {
		if len(cur) == 0 {
			return
		}
		blocks = append(blocks, blockFromSpans(page, cur, median))
		cur = nil
		curSize = 0
	}

	for _, line := range lines {
		lineSize := dominantFontSize(line)
		if len(cur) == 0 {
			cur = append(cur, line...)
			curSize = lineSize
			continue
		}
		prev := cur[len(cur)-1]
		gap := prev.bbox.Bottom - line[0].bbox.Top
		height := math.Max(prev.bbox.Top-prev.bbox.Bottom, 1)
		sizeJump := curSize > 0 && lineSize > 0 &&
			(lineSize >= curSize*headingSizeFactor || curSize >= lineSize*headingSizeFactor)
		if gap > height*lineGapFactor || sizeJump {
			flush()
		}
		cur = append(cur, line...)
		if lineSize > 0 {
			curSize = lineSize
		}
	}
	flush()
	return blocks
}

func groupLines(spans []span) [][]span {
	var lines [][]span
	for _, s := range spans {
		if len(lines) == 0 {
			lines = append(lines, []span{s})
			continue
		}
		last := lines[len(lines)-1]
		ref := last[0]
		tol := math.Max(math.Abs(ref.bbox.Top-ref.bbox.Bottom)*0.5, 2)
		if math.Abs(s.bbox.Top-ref.bbox.Top) <= tol {
			lines[len(lines)-1] = append(last, s)
			continue
		}
		lines = append(lines, []span{s})
	}
	for i := range lines {
		sort.SliceStable(lines[i], func(a, b int) bool {
			return lines[i][a].bbox.Left < lines[i][b].bbox.Left
		})
	}
	return lines
}

func blockFromSpans(page int, spans []span, medianFont float64) document.Block {
	var lines []string
	var line []string
	var bbox *document.BBox
	var top float64
	var haveTop bool
	size := dominantFontSize(spans)

	flushLine := func() {
		if len(line) == 0 {
			return
		}
		lines = append(lines, strings.Join(line, " "))
		line = nil
	}

	for _, s := range spans {
		if !haveTop || !nearlyEqual(s.bbox.Top, top, 1) {
			flushLine()
			top = s.bbox.Top
			haveTop = true
		}
		line = append(line, s.text)
		bbox = unionBBox(bbox, s.bbox)
	}
	flushLine()

	text := strings.TrimSpace(strings.Join(lines, "\n"))
	kind := document.BlockParagraph
	if isHeading(text, size, medianFont) {
		kind = document.BlockHeading
	} else if looksLikeListItem(text) {
		// Keep list markers in paragraph text; Block has no dedicated list kind.
		kind = document.BlockParagraph
	}
	return document.Block{Kind: kind, Text: text, Page: page, BBox: bbox}
}

func isHeading(text string, size, median float64) bool {
	if text == "" || runeCount(text) > maxHeadingRunes {
		return false
	}
	if strings.Contains(text, "\n") {
		return false
	}
	if median <= 0 || size < median*headingSizeFactor {
		return false
	}
	return true
}

func looksLikeListItem(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	r := []rune(text)
	switch r[0] {
	case '•', '·', '▪', '○', '-', '*', '–', '—':
		return len(r) > 1
	}
	// "1." / "12)" style markers
	i := 0
	for i < len(r) && unicode.IsDigit(r[i]) {
		i++
	}
	if i == 0 || i >= len(r) {
		return false
	}
	return r[i] == '.' || r[i] == ')' || r[i] == '-'
}

func spanFontSize(r *responses.GetPageTextStructuredRect) float64 {
	if r.FontInformation == nil {
		return 0
	}
	if r.FontInformation.RenderedSize > 0 {
		return r.FontInformation.RenderedSize
	}
	return r.FontInformation.Size
}

func dominantFontSize(spans []span) float64 {
	sizes := make([]float64, 0, len(spans))
	for _, s := range spans {
		if s.fontSize > 0 {
			sizes = append(sizes, s.fontSize)
		}
	}
	return medianFloat(sizes)
}

func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 0 {
		return (cp[mid-1] + cp[mid]) / 2
	}
	return cp[mid]
}

func unionBBox(base *document.BBox, next document.BBox) *document.BBox {
	if base == nil {
		b := next
		return &b
	}
	if next.Left < base.Left {
		base.Left = next.Left
	}
	if next.Right > base.Right {
		base.Right = next.Right
	}
	if next.Top > base.Top {
		base.Top = next.Top
	}
	if next.Bottom < base.Bottom {
		base.Bottom = next.Bottom
	}
	return base
}

func nearlyEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func runeCount(s string) int {
	return len([]rune(s))
}
