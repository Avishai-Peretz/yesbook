// Package documentai extracts scans and tables with a Google Document AI
// OCR or Layout processor.
package documentai

import (
	"context"
	"fmt"
	"strings"

	documentai "cloud.google.com/go/documentai/apiv1"
	"cloud.google.com/go/documentai/apiv1/documentaipb"
	"google.golang.org/api/option"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

type Extractor struct {
	client      *documentai.DocumentProcessorClient
	processor   string
	version     string
	costPerPage int64
}

// New connects to the regional endpoint of processor, a full resource name
// such as projects/P/locations/eu/processors/ID. version pins the processor
// version ID used in the pipeline version; costPerPageMicros feeds the
// spend cap.
func New(ctx context.Context, location, processor, version string, costPerPageMicros int64) (*Extractor, error) {
	client, err := documentai.NewDocumentProcessorClient(ctx,
		option.WithEndpoint(location+"-documentai.googleapis.com:443"))
	if err != nil {
		return nil, fmt.Errorf("create document ai client: %w", err)
	}
	name := processor
	if version != "" {
		name = processor + "/processorVersions/" + version
	}
	return &Extractor{client: client, processor: name, version: version, costPerPage: costPerPageMicros}, nil
}

func (e *Extractor) Close() error { return e.client.Close() }

func (e *Extractor) Name() string { return "documentai" }

func (e *Extractor) Version() string {
	if e.version == "" {
		return "default"
	}
	return e.version
}

func (e *Extractor) Extract(ctx context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	pages := make([]int32, len(req.Target.Pages))
	for i, p := range req.Target.Pages {
		pages[i] = int32(p) //nolint:gosec // page numbers are bounded by the pipeline's page limit
	}
	resp, err := e.client.ProcessDocument(ctx, &documentaipb.ProcessRequest{
		Name: e.processor,
		Source: &documentaipb.ProcessRequest_RawDocument{
			RawDocument: &documentaipb.RawDocument{Content: req.PDF, MimeType: document.ContentTypePDF},
		},
		ProcessOptions: &documentaipb.ProcessOptions{
			PageRange: &documentaipb.ProcessOptions_IndividualPageSelector_{
				IndividualPageSelector: &documentaipb.ProcessOptions_IndividualPageSelector{Pages: pages},
			},
		},
	})
	if err != nil {
		return document.Extraction{}, fmt.Errorf("document ai process: %w", err)
	}
	cost := e.costPerPage * int64(len(pages))
	return document.Extraction{Blocks: blocks(resp.GetDocument(), req.Target.Pages), CostMicros: cost}, nil
}

// blocks maps processed pages back to target pages, emits tables as rows,
// and drops paragraphs that only repeat table text.
func blocks(doc *documentaipb.Document, targetPages []int) []document.Block {
	text := []rune(doc.GetText())
	var out []document.Block
	for i, page := range doc.GetPages() {
		n := int(page.GetPageNumber())
		if len(doc.GetPages()) == len(targetPages) {
			n = targetPages[i] //nolint:gosec // guarded by the length check above
		}
		var tables [][2]int64
		for _, t := range page.GetTables() {
			tables = append(tables, span(t.GetLayout()))
			var rows [][]string
			for _, r := range append(t.GetHeaderRows(), t.GetBodyRows()...) {
				var row []string
				for _, c := range r.GetCells() {
					row = append(row, anchorText(text, c.GetLayout()))
				}
				rows = append(rows, row)
			}
			if len(rows) > 0 {
				out = append(out, document.Block{Kind: document.BlockTable, Rows: rows, Page: n})
			}
		}
		for _, p := range page.GetParagraphs() {
			if within(span(p.GetLayout()), tables) {
				continue
			}
			if s := anchorText(text, p.GetLayout()); s != "" {
				out = append(out, document.Block{Kind: document.BlockParagraph, Text: s, Page: n})
			}
		}
	}
	return out
}

// anchorText resolves a layout's text segments. Document AI indexes
// Document.text by code point.
func anchorText(text []rune, l *documentaipb.Document_Page_Layout) string {
	var sb strings.Builder
	for _, seg := range l.GetTextAnchor().GetTextSegments() {
		start, end := seg.GetStartIndex(), seg.GetEndIndex()
		if start < 0 || end > int64(len(text)) || start >= end {
			continue
		}
		sb.WriteString(string(text[start:end]))
	}
	return strings.TrimSpace(sb.String())
}

func span(l *documentaipb.Document_Page_Layout) [2]int64 {
	segs := l.GetTextAnchor().GetTextSegments()
	if len(segs) == 0 {
		return [2]int64{-1, -1}
	}
	return [2]int64{segs[0].GetStartIndex(), segs[len(segs)-1].GetEndIndex()}
}

func within(s [2]int64, ranges [][2]int64) bool {
	for _, r := range ranges {
		if s[0] >= r[0] && s[1] <= r[1] && r[0] >= 0 {
			return true
		}
	}
	return false
}
