package docling_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/docling"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

const response = `{
  "status": "success",
  "errors": [],
  "document": {"json_content": {
    "body": {"children": [{"$ref": "#/texts/0"}, {"$ref": "#/groups/0"}, {"$ref": "#/tables/0"}, {"$ref": "#/pictures/0"}]},
    "groups": [{"children": [{"$ref": "#/texts/1"}, {"$ref": "#/texts/2"}]}],
    "texts": [
      {"label": "section_header", "text": "פרק 1", "prov": [{"page_no": 3, "bbox": {"l": 10, "t": 700, "r": 200, "b": 680}}]},
      {"label": "text", "text": "שלום עולם", "prov": [{"page_no": 3}]},
      {"label": "page_footer", "text": "3", "prov": [{"page_no": 3}]}
    ],
    "tables": [{"prov": [{"page_no": 3}], "data": {"num_rows": 2, "num_cols": 2, "table_cells": [
      {"text": "שם", "start_row_offset_idx": 0, "start_col_offset_idx": 0},
      {"text": "ציון", "start_row_offset_idx": 0, "start_col_offset_idx": 1},
      {"text": "דנה", "start_row_offset_idx": 1, "start_col_offset_idx": 0},
      {"text": "90", "start_row_offset_idx": 1, "start_col_offset_idx": 1}
    ]}}]
  }}
}`

func TestExtract(t *testing.T) {
	var form map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/convert/file" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		form = r.MultipartForm.Value
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	e := docling.New(srv.URL, "v1", "", srv.Client())
	target := document.Target{ID: "p0003", Pages: []int{3}, Content: document.ContentScan, Language: "he"}
	ex, err := e.Extract(context.Background(), processing.ExtractRequest{PDF: []byte("%PDF-1.7"), Target: target})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(form["page_range"], []string{"3", "3"}) || form["force_ocr"][0] != "true" || !slices.Contains(form["ocr_lang"], "he") {
		t.Errorf("form = %v", form)
	}
	if len(ex.Blocks) != 3 {
		t.Fatalf("blocks = %+v, want heading, paragraph and table", ex.Blocks)
	}
	if b := ex.Blocks[0]; b.Kind != document.BlockHeading || b.Page != 3 || b.BBox == nil || b.BBox.Left != 10 {
		t.Errorf("heading = %+v", b)
	}
	if b := ex.Blocks[1]; b.Kind != document.BlockParagraph || b.Text != "שלום עולם" {
		t.Errorf("paragraph = %+v", b)
	}
	if b := ex.Blocks[2]; b.Kind != document.BlockTable || b.Rows[1][1] != "90" {
		t.Errorf("table = %+v", b)
	}
}

func TestExtractReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status": "failure", "errors": [{"message": "bad pdf"}], "document": {}}`))
	}))
	defer srv.Close()

	_, err := docling.New(srv.URL, "v1", "", srv.Client()).Extract(context.Background(),
		processing.ExtractRequest{PDF: []byte("%PDF"), Target: document.Target{Pages: []int{1}}})
	if err == nil {
		t.Fatal("want error for failed conversion")
	}
}
