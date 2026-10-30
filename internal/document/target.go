package document

// TargetKind is the unit of a PDF one extractor processes.
type TargetKind string

const (
	TargetPage     TargetKind = "page"
	TargetRegion   TargetKind = "region"
	TargetDocument TargetKind = "document"
)

// ContentKind is the kind of content a target holds, used for routing.
type ContentKind string

const (
	ContentText        ContentKind = "text"
	ContentScan        ContentKind = "scan"
	ContentTable       ContentKind = "table"
	ContentHandwriting ContentKind = "handwriting"
	ContentFormula     ContentKind = "formula"
	ContentDiagram     ContentKind = "diagram"
	ContentBlank       ContentKind = "blank"
)

// Target is one page, region or whole document to extract, with the
// context and page mapping extractors need.
type Target struct {
	ID             string      `json:"id"`
	Kind           TargetKind  `json:"kind"`
	Pages          []int       `json:"pages"`
	Content        ContentKind `json:"content"`
	Language       string      `json:"language,omitempty"`
	TextLayer      string      `json:"text_layer,omitempty"`
	TextLayerChars int         `json:"text_layer_chars"`
	// EmbeddedImages is the Inspect count of PDF image objects on the
	// target's pages. Validation uses it to detect extractors that
	// returned text but dropped figures.
	EmbeddedImages int    `json:"embedded_images,omitempty"`
	Context        string `json:"context,omitempty"`
}

// FirstPage returns the 1-based page the target starts on.
func (t Target) FirstPage() int {
	if len(t.Pages) == 0 {
		return 0
	}
	return t.Pages[0]
}

// Contains reports whether page is one of the target's pages.
func (t Target) Contains(page int) bool {
	for _, p := range t.Pages {
		if p == page {
			return true
		}
	}
	return false
}
