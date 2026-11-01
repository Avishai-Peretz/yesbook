// Package vision holds what the multimodal LLM extractors share: the page
// rendering port, the transcription prompt, the response schema and parsing.
package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
)

// DPI balances legibility of handwriting and formulas against image tokens.
const DPI = 150

type Renderer interface {
	RenderPNG(ctx context.Context, pdf []byte, page, dpi int) ([]byte, error)
}

// Pricing is the model price in micro-dollars per million tokens, used to
// enforce the per-document spend cap.
type Pricing struct {
	InputPerMillion  int64
	OutputPerMillion int64
}

func (p Pricing) Cost(inputTokens, outputTokens int64) int64 {
	return (inputTokens*p.InputPerMillion + outputTokens*p.OutputPerMillion) / 1_000_000
}

// Page is one rendered page of a target.
type Page struct {
	Number int
	PNG    []byte
}

// RenderTarget renders every page of the target.
func RenderTarget(ctx context.Context, r Renderer, pdf []byte, t document.Target) ([]Page, error) {
	pages := make([]Page, 0, len(t.Pages))
	for _, n := range t.Pages {
		img, err := r.RenderPNG(ctx, pdf, n, DPI)
		if err != nil {
			return nil, err
		}
		pages = append(pages, Page{Number: n, PNG: img})
	}
	return pages, nil
}

const SystemPrompt = `You transcribe pages of educational documents into structured JSON.
Transcribe exactly what is on the page; never summarize, translate or invent content.
Write Hebrew and other right-to-left text in logical reading order, as a person would type it.
Write mathematical formulas in LaTeX without surrounding dollar signs.
Return tables as rows of cells, including the header row.
For diagrams, photos and drawings, emit a figure block whose text is a short factual description.`

// Prompt asks for the content of the target's pages, with optional text
// from neighbouring pages to resolve sentences and tables that continue.
func Prompt(t document.Target) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Transcribe page %s of the document. The page is expected to contain %s content", pageList(t.Pages), t.Content)
	if t.Language != "" {
		fmt.Fprintf(&sb, " in language %q", t.Language)
	}
	sb.WriteString(".\nReturn one block per paragraph, heading, table, formula or figure, in reading order, each with the page number it appears on.")
	if t.Context != "" {
		sb.WriteString("\n\nText of the neighbouring pages, for context only. Do not transcribe it:\n<context>\n")
		sb.WriteString(t.Context)
		sb.WriteString("\n</context>")
	}
	return sb.String()
}

func pageList(pages []int) string {
	s := make([]string, len(pages))
	for i, p := range pages {
		s[i] = fmt.Sprint(p)
	}
	return strings.Join(s, ", ")
}

// Schema is the JSON schema of the model response.
func Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             []string{"blocks"},
		"additionalProperties": false,
		"properties": map[string]any{
			"blocks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"required":             []string{"kind", "page", "text", "rows"},
					"additionalProperties": false,
					"properties": map[string]any{
						"kind": map[string]any{"type": "string", "enum": []string{
							string(document.BlockParagraph), string(document.BlockHeading), string(document.BlockTable),
							string(document.BlockFormula), string(document.BlockFigure),
						}},
						"page": map[string]any{"type": "integer"},
						"text": map[string]any{"type": "string", "description": "Block text; empty for tables."},
						"rows": map[string]any{
							"type":        "array",
							"description": "Table rows; empty for other kinds.",
							"items":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
			},
		},
	}
}

type response struct {
	Blocks []struct {
		Kind string     `json:"kind"`
		Page int        `json:"page"`
		Text string     `json:"text"`
		Rows [][]string `json:"rows"`
	} `json:"blocks"`
}

// Parse converts a model response into blocks. Blocks the model anchored to
// a page outside the target are kept as-is so validation flags them.
func Parse(raw string, t document.Target) ([]document.Block, error) {
	var r response
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	blocks := make([]document.Block, 0, len(r.Blocks))
	for _, b := range r.Blocks {
		kind := document.BlockKind(b.Kind)
		block := document.Block{Kind: kind, Page: b.Page, Text: strings.TrimSpace(b.Text)}
		if block.Page == 0 {
			block.Page = t.FirstPage()
		}
		if kind == document.BlockTable {
			block.Text, block.Rows = "", b.Rows
		} else if block.Text == "" {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}
