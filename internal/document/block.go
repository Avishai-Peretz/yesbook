package document

import "strings"

type BlockKind string

const (
	BlockParagraph BlockKind = "paragraph"
	BlockHeading   BlockKind = "heading"
	BlockTable     BlockKind = "table"
	BlockFormula   BlockKind = "formula"
	BlockFigure    BlockKind = "figure"
)

type Direction string

const (
	DirectionLTR Direction = "ltr"
	DirectionRTL Direction = "rtl"
)

// BBox is a rectangle in PDF points.
type BBox struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

// Provenance records where a block came from.
type Provenance struct {
	TargetID         string `json:"target_id"`
	Extractor        string `json:"extractor"`
	ExtractorVersion string `json:"extractor_version"`
	AttemptID        string `json:"attempt_id"`
}

// AssetRef points at bytes stored in the derived bucket. Content API signs it.
type AssetRef struct {
	Object      string `json:"object"`
	ContentType string `json:"content_type,omitempty"`
}

// Block is one unit of extracted content anchored to its source page.
type Block struct {
	Kind       BlockKind  `json:"kind"`
	Text       string     `json:"text,omitempty"`
	Rows       [][]string `json:"rows,omitempty"`
	Page       int        `json:"page"`
	EndPage    int        `json:"end_page,omitempty"`
	Direction  Direction  `json:"direction,omitempty"`
	BBox       *BBox      `json:"bbox,omitempty"`
	Asset      *AssetRef  `json:"asset,omitempty"`
	Provenance Provenance `json:"provenance"`

	// AssetData holds image bytes before publish writes them to object storage.
	// JSON encodes []byte as base64 so evidence snapshots can resume; commit
	// clears it before writing content.json.
	AssetData []byte `json:"asset_data,omitempty"`
}

// PlainText returns the block's text, with table cells joined by spaces.
func (b Block) PlainText() string {
	if b.Kind != BlockTable {
		return b.Text
	}
	var sb strings.Builder
	for _, row := range b.Rows {
		sb.WriteString(strings.Join(row, " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Extraction is the output of one extractor for one target.
type Extraction struct {
	TargetID         string  `json:"target_id"`
	Extractor        string  `json:"extractor"`
	ExtractorVersion string  `json:"extractor_version"`
	Blocks           []Block `json:"blocks"`
	CostMicros       int64   `json:"cost_micros"`
}

// Text returns the plain text of all blocks in order.
func (e Extraction) Text() string {
	var sb strings.Builder
	for _, b := range e.Blocks {
		sb.WriteString(b.PlainText())
		sb.WriteByte('\n')
	}
	return sb.String()
}
