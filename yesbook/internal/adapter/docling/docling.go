// Package docling extracts scans and tables with a self-hosted docling-serve
// instance (OCR, layout and table structure models).
package docling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

const maxResponseBytes = 64 << 20

type Extractor struct {
	baseURL string
	version string
	apiKey  string
	client  *http.Client
}

// New targets docling-serve at baseURL. version pins the deployed image tag;
// apiKey is sent as X-Api-Key when the server requires one.
func New(baseURL, version, apiKey string, client *http.Client) *Extractor {
	if client == nil {
		client = http.DefaultClient
	}
	return &Extractor{baseURL: strings.TrimRight(baseURL, "/"), version: version, apiKey: apiKey, client: client}
}

func (e *Extractor) Name() string    { return "docling" }
func (e *Extractor) Version() string { return e.version }

func (e *Extractor) Extract(ctx context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	body, contentType, err := form(req)
	if err != nil {
		return document.Extraction{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/convert/file", body)
	if err != nil {
		return document.Extraction{}, fmt.Errorf("build docling request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")
	if e.apiKey != "" {
		httpReq.Header.Set("X-Api-Key", e.apiKey)
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return document.Extraction{}, fmt.Errorf("call docling: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readLimited(resp.Body, maxResponseBytes)
	if err != nil {
		return document.Extraction{}, fmt.Errorf("read docling response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return document.Extraction{}, fmt.Errorf("docling returned %s: %s", resp.Status, truncate(raw, 300))
	}

	var out convertResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return document.Extraction{}, fmt.Errorf("decode docling response: %w", err)
	}
	if out.Status != "success" && out.Status != "partial_success" {
		return document.Extraction{}, fmt.Errorf("docling conversion %s: %s", out.Status, string(out.Errors))
	}
	return document.Extraction{Blocks: out.Document.JSONContent.blocks(req.Target)}, nil
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("response too large")
	}
	return data, nil
}

func form(req processing.ExtractRequest) (io.Reader, string, error) {
	if len(req.Target.Pages) == 0 {
		return nil, "", errors.New("docling target has no pages")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("files", "document.pdf")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(req.PDF); err != nil {
		return nil, "", err
	}
	t := req.Target
	fields := [][2]string{
		{"to_formats", "json"},
		{"do_ocr", "true"},
		{"force_ocr", strconv.FormatBool(t.Content != document.ContentText)},
		{"table_mode", "accurate"},
		{"image_export_mode", "embedded"},
		{"page_range", strconv.Itoa(t.Pages[0])},
		{"page_range", strconv.Itoa(t.Pages[len(t.Pages)-1])},
	}
	for _, lang := range ocrLanguages(t.Language) {
		fields = append(fields, [2]string{"ocr_lang", lang})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// ocrLanguages always includes English, which most Hebrew course material
// mixes in for terms and formulas.
func ocrLanguages(lang string) []string {
	if lang == "he" {
		return []string{"he", "en"}
	}
	return []string{"en"}
}

type convertResponse struct {
	Document struct {
		JSONContent doclingDocument `json:"json_content"`
	} `json:"document"`
	Status string          `json:"status"`
	Errors json.RawMessage `json:"errors"`
}

type ref struct {
	Ref string `json:"$ref"`
}

type prov struct {
	PageNo int `json:"page_no"`
	BBox   struct {
		L float64 `json:"l"`
		T float64 `json:"t"`
		R float64 `json:"r"`
		B float64 `json:"b"`
	} `json:"bbox"`
}

type textItem struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Prov  []prov `json:"prov"`
}

type tableItem struct {
	Prov []prov `json:"prov"`
	Data struct {
		NumRows int `json:"num_rows"`
		NumCols int `json:"num_cols"`
		Cells   []struct {
			Text string `json:"text"`
			Row  int    `json:"start_row_offset_idx"`
			Col  int    `json:"start_col_offset_idx"`
		} `json:"table_cells"`
	} `json:"data"`
}

type group struct {
	Children []ref `json:"children"`
}

type pictureItem struct {
	Prov     []prov `json:"prov"`
	Captions []ref  `json:"captions"`
	Image    *struct {
		Mimetype string `json:"mimetype"`
		URI      string `json:"uri"`
	} `json:"image"`
}

// doclingDocument is the subset of the DoclingDocument JSON format used here.
type doclingDocument struct {
	Body     group         `json:"body"`
	Groups   []group       `json:"groups"`
	Texts    []textItem    `json:"texts"`
	Tables   []tableItem   `json:"tables"`
	Pictures []pictureItem `json:"pictures"`
}

// blocks walks the body tree in reading order.
func (d doclingDocument) blocks(t document.Target) []document.Block {
	var out []document.Block
	var walk func(children []ref, depth int)
	walk = func(children []ref, depth int) {
		if depth > 32 {
			return
		}
		for _, c := range children {
			kind, idx, ok := parseRef(c.Ref)
			if !ok {
				continue
			}
			switch {
			case kind == "texts" && idx < len(d.Texts):
				if b, ok := textBlock(d.Texts[idx], t); ok {
					out = append(out, b)
				}
			case kind == "tables" && idx < len(d.Tables):
				if b, ok := tableBlock(d.Tables[idx], t); ok {
					out = append(out, b)
				}
			case kind == "pictures" && idx < len(d.Pictures):
				if b, ok := pictureBlock(d.Pictures[idx], d.Texts, t); ok {
					out = append(out, b)
				}
			case kind == "groups" && idx < len(d.Groups):
				walk(d.Groups[idx].Children, depth+1)
			}
		}
	}
	walk(d.Body.Children, 0)
	return out
}

func pictureBlock(it pictureItem, texts []textItem, t document.Target) (document.Block, bool) {
	caption := ""
	for _, c := range it.Captions {
		kind, idx, ok := parseRef(c.Ref)
		if !ok || kind != "texts" || idx >= len(texts) {
			continue
		}
		caption = strings.TrimSpace(texts[idx].Text)
		if caption != "" {
			break
		}
	}
	b := document.Block{Kind: document.BlockFigure, Text: caption}
	anchor(&b, it.Prov, t)
	if it.Image != nil {
		if data, contentType, ok := decodeEmbeddedImage(it.Image.URI, it.Image.Mimetype); ok {
			b.AssetData = data
			b.Asset = &document.AssetRef{ContentType: contentType}
		}
	}
	if b.Text == "" && len(b.AssetData) == 0 {
		return document.Block{}, false
	}
	return b, true
}

func decodeEmbeddedImage(uri, mimetype string) ([]byte, string, bool) {
	contentType := strings.TrimSpace(mimetype)
	raw := strings.TrimSpace(uri)
	if raw == "" {
		return nil, "", false
	}
	if strings.HasPrefix(raw, "data:") {
		header, payload, ok := strings.Cut(raw, ",")
		if !ok {
			return nil, "", false
		}
		if contentType == "" {
			meta := strings.TrimPrefix(header, "data:")
			contentType, _, _ = strings.Cut(meta, ";")
		}
		raw = payload
	}
	if contentType == "" {
		contentType = "image/png"
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	return data, contentType, true
}

func parseRef(s string) (string, int, bool) {
	parts := strings.Split(strings.TrimPrefix(s, "#/"), "/")
	if len(parts) != 2 {
		return "", 0, false
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return "", 0, false
	}
	return parts[0], idx, true
}

func textBlock(it textItem, t document.Target) (document.Block, bool) {
	text := strings.TrimSpace(it.Text)
	if text == "" {
		return document.Block{}, false
	}
	kind := document.BlockParagraph
	switch it.Label {
	case "title", "section_header":
		kind = document.BlockHeading
	case "formula":
		kind = document.BlockFormula
	case "list_item":
		// Preserve marker text as a paragraph; document.Block has no list kind.
		kind = document.BlockParagraph
	case "page_header", "page_footer":
		return document.Block{}, false
	}
	b := document.Block{Kind: kind, Text: text}
	anchor(&b, it.Prov, t)
	return b, true
}

func tableBlock(it tableItem, t document.Target) (document.Block, bool) {
	rows, cols := it.Data.NumRows, it.Data.NumCols
	if rows <= 0 || cols <= 0 || rows*cols > 100_000 {
		return document.Block{}, false
	}
	grid := make([][]string, rows)
	for i := range grid {
		grid[i] = make([]string, cols)
	}
	for _, c := range it.Data.Cells {
		if c.Row >= 0 && c.Row < rows && c.Col >= 0 && c.Col < cols {
			grid[c.Row][c.Col] = strings.TrimSpace(c.Text)
		}
	}
	b := document.Block{Kind: document.BlockTable, Rows: grid}
	anchor(&b, it.Prov, t)
	return b, true
}

// anchor sets page and bounding box from the item's first provenance. A
// single-page target owns everything docling returns for it.
func anchor(b *document.Block, provs []prov, t document.Target) {
	if len(provs) == 0 {
		b.Page = t.FirstPage()
		return
	}
	p := provs[0]
	b.Page = p.PageNo
	if len(t.Pages) == 1 {
		b.Page = t.Pages[0]
	}
	b.BBox = &document.BBox{Left: p.BBox.L, Top: p.BBox.T, Right: p.BBox.R, Bottom: p.BBox.B}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
