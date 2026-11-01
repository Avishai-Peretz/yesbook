package document

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

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
	Context        string      `json:"context,omitempty"`
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

// Block is one unit of extracted content anchored to its source page.
type Block struct {
	Kind       BlockKind  `json:"kind"`
	Text       string     `json:"text,omitempty"`
	Rows       [][]string `json:"rows,omitempty"`
	Page       int        `json:"page"`
	EndPage    int        `json:"end_page,omitempty"`
	Direction  Direction  `json:"direction,omitempty"`
	BBox       *BBox      `json:"bbox,omitempty"`
	Provenance Provenance `json:"provenance"`
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

// Verdict is the validation result for one extraction.
type Verdict string

const (
	VerdictAccept Verdict = "accept"
	VerdictRetry  Verdict = "retry"
	VerdictReview Verdict = "review"
	VerdictReject Verdict = "reject"
)

type Check struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Detail string  `json:"detail,omitempty"`
}

// QualityResult is the validation outcome stored for every target.
type QualityResult struct {
	TargetID         string  `json:"target_id"`
	Extractor        string  `json:"extractor"`
	ExtractorVersion string  `json:"extractor_version"`
	Verdict          Verdict `json:"verdict"`
	Checks           []Check `json:"checks"`
	Reason           string  `json:"reason,omitempty"`
}

// ReviewItem is a region that did not pass validation.
type ReviewItem struct {
	TargetID string `json:"target_id"`
	Pages    []int  `json:"pages"`
	Reason   string `json:"reason"`
}

// Version is an accepted, published extraction of a file.
type Version struct {
	ID              string
	TenantID        string
	JobID           string
	FileHash        string
	PipelineVersion string
	Status          Status
	Coverage        float64
	ContentObject   string
	OriginalObject  string
	CreatedAt       time.Time
}

// Content is the published JSON document of a version.
type Content struct {
	VersionID       string       `json:"version_id"`
	JobID           string       `json:"job_id"`
	Status          Status       `json:"status"`
	Coverage        float64      `json:"coverage"`
	PipelineVersion string       `json:"pipeline_version"`
	Pages           int          `json:"pages"`
	Blocks          []Block      `json:"blocks"`
	Review          []ReviewItem `json:"review,omitempty"`
	Missing         []ReviewItem `json:"missing,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}

// CacheKey identifies the result of processing a file with one pipeline
// configuration, so a hash match only reuses output produced the same way.
func CacheKey(tenantID, fileHash, pipelineVersion string) string {
	sum := sha256.Sum256([]byte(tenantID + "\n" + fileHash + "\n" + pipelineVersion))
	return hex.EncodeToString(sum[:])
}
