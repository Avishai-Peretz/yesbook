package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/avi-p/yesbook/internal/document"
)

func TestRegisterExtractionModeAndDelete(t *testing.T) {
	e := newEnv(t)

	var reg struct {
		Job struct {
			ID             string `json:"id"`
			ExtractionMode string `json:"extraction_mode"`
		} `json:"job"`
		Upload struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		} `json:"upload"`
	}
	body, _ := json.Marshal(map[string]any{
		"file_name":       "mode.pdf",
		"content_type":    document.ContentTypePDF,
		"size_bytes":      len(pdf),
		"extraction_mode": "pdfium",
	})
	if code := call(t, "POST", e.docs.URL+"/v1/documents", keyA, body, nil, &reg); code != http.StatusCreated {
		t.Fatalf("register status = %d", code)
	}
	if reg.Job.ExtractionMode != "pdfium" {
		t.Fatalf("extraction_mode = %q", reg.Job.ExtractionMode)
	}

	bad, _ := json.Marshal(map[string]any{
		"file_name": "bad.pdf", "content_type": document.ContentTypePDF, "size_bytes": 10,
		"extraction_mode": "docling",
	})
	if code := call(t, "POST", e.docs.URL+"/v1/documents", keyA, bad, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("unconfigured mode status = %d", code)
	}

	if code := call(t, reg.Upload.Method, reg.Upload.URL, "", pdf, reg.Upload.Headers, nil); code >= 300 {
		t.Fatalf("upload status = %d", code)
	}
	submit, _ := json.Marshal(map[string]any{"extraction_mode": "pdfium"})
	if code := call(t, "POST", e.docs.URL+"/v1/documents/"+reg.Job.ID+"/submit", keyA, submit, nil, nil); code != http.StatusAccepted {
		t.Fatalf("submit status = %d", code)
	}
	if code := call(t, "DELETE", e.docs.URL+"/v1/documents/"+reg.Job.ID, keyA, nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d", code)
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents/"+reg.Job.ID, keyA, nil, nil, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", code)
	}
}

func TestRegisterDefaultMode(t *testing.T) {
	e := newEnv(t)
	var reg struct {
		Job struct {
			ExtractionMode string `json:"extraction_mode"`
		} `json:"job"`
	}
	body := []byte(`{"file_name":"a.pdf","content_type":"application/pdf","size_bytes":` + strconv.Itoa(len(pdf)) + `}`)
	if code := call(t, "POST", e.docs.URL+"/v1/documents", keyA, body, nil, &reg); code != http.StatusCreated {
		t.Fatalf("register status = %d", code)
	}
	if reg.Job.ExtractionMode != "automatic" {
		t.Fatalf("default mode = %q", reg.Job.ExtractionMode)
	}
}
