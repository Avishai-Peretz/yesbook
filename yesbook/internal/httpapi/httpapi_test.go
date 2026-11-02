package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/processing"
)

const (
	keyA = "tenant-a-key-0123456789"
	keyB = "tenant-b-key-0123456789"
)

var pdf = []byte("%PDF-1.7 fake document body")

type inspector struct{}

func (inspector) Inspect(context.Context, []byte) (processing.PDFInfo, error) {
	return processing.PDFInfo{Pages: []processing.PageInfo{{Number: 1, Text: "Photosynthesis converts light energy into chemical energy"}}}, nil
}

type echo struct{}

func (echo) Name() string    { return "pdfium" }
func (echo) Version() string { return "test" }
func (echo) Extract(_ context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	return document.Extraction{Blocks: []document.Block{{
		Kind: document.BlockParagraph,
		Text: req.Target.TextLayer,
		BBox: &document.BBox{Left: 72, Top: 720, Right: 400, Bottom: 700},
	}}}, nil
}

// inlineWorkflow runs the pipeline synchronously instead of Cloud Workflows.
type inlineWorkflow struct{ p *processing.Pipeline }

func (w inlineWorkflow) Start(ctx context.Context, tenantID, jobID string) error {
	_, err := w.p.Process(ctx, tenantID, jobID, "test-worker")
	return err
}

type env struct {
	docs, content, blobs *httptest.Server
}

func newEnv(t *testing.T) env {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := memory.New()
	blobSrv := httptest.NewUnstartedServer(nil)
	blobs, err := localblob.New(t.TempDir(), "http://"+blobSrv.Listener.Addr().String(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	blobSrv.Config.Handler = blobs.Handler()
	blobSrv.Start()
	t.Cleanup(blobSrv.Close)

	pipeline, err := processing.NewPipeline(processing.Dependencies{
		Jobs: store, Versions: store, Attempts: store, Progress: store, Objects: blobs, Inspector: inspector{}, Logger: log,
		Router: processing.NewRouter(map[document.ContentKind][]processing.Extractor{document.ContentText: {echo{}}}),
	}, processing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.ParseAPIKeys("tenant-a:" + keyA + ",tenant-b:" + keyB)
	if err != nil {
		t.Fatal(err)
	}
	docs := httptest.NewServer(httpapi.NewDocumentHandler(ingest.NewService(store, blobs, inlineWorkflow{pipeline},
		ingest.WithConfiguredModes(map[string]bool{"pdfium": true})), store, keys, log))
	t.Cleanup(docs.Close)
	cont := httptest.NewServer(httpapi.NewContentHandler(content.NewService(store, store, blobs, time.Minute), keys, log))
	t.Cleanup(cont.Close)
	return env{docs: docs, content: cont, blobs: blobSrv}
}

func call(t *testing.T, method, url, key string, body []byte, headers map[string]string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 300 {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestUploadProcessAndReadContent(t *testing.T) {
	e := newEnv(t)

	var reg struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
		Upload struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		} `json:"upload"`
	}
	body := []byte(`{"file_name": "biology.pdf", "content_type": "application/pdf", "size_bytes": ` + strconv.Itoa(len(pdf)) + `}`)
	if code := call(t, "POST", e.docs.URL+"/v1/documents", keyA, body, nil, &reg); code != http.StatusCreated {
		t.Fatalf("register status = %d", code)
	}
	id := reg.Job.ID

	if code := call(t, "POST", e.docs.URL+"/v1/documents/"+id+"/submit", keyA, nil, nil, nil); code != http.StatusConflict {
		t.Errorf("submit before upload = %d, want 409", code)
	}
	if code := call(t, reg.Upload.Method, reg.Upload.URL, "", pdf, reg.Upload.Headers, nil); code >= 300 {
		t.Fatalf("upload status = %d", code)
	}
	if code := call(t, "GET", e.content.URL+"/v1/documents/"+id+"/content", keyA, nil, nil, nil); code != http.StatusConflict {
		t.Errorf("content before processing = %d, want 409", code)
	}
	if code := call(t, "POST", e.docs.URL+"/v1/documents/"+id+"/submit", keyA, nil, nil, nil); code != http.StatusAccepted {
		t.Fatalf("submit status = %d", code)
	}

	var job struct {
		Status    string `json:"status"`
		VersionID string `json:"version_id"`
	}
	call(t, "GET", e.docs.URL+"/v1/documents/"+id, keyA, nil, nil, &job)
	if job.Status != "ready" || job.VersionID == "" {
		t.Fatalf("job = %+v, want ready with a version", job)
	}

	var res struct {
		Content struct {
			Status string `json:"status"`
			Blocks []struct {
				Text       string `json:"text"`
				Page       int    `json:"page"`
				Provenance struct {
					Extractor string `json:"extractor"`
				} `json:"provenance"`
			} `json:"blocks"`
		} `json:"content"`
		Original struct {
			URL string `json:"url"`
		} `json:"original"`
	}
	if code := call(t, "GET", e.content.URL+"/v1/documents/"+id+"/content", keyA, nil, nil, &res); code != http.StatusOK {
		t.Fatalf("content status = %d", code)
	}
	if len(res.Content.Blocks) != 1 || !strings.Contains(res.Content.Blocks[0].Text, "Photosynthesis") ||
		res.Content.Blocks[0].Page != 1 || res.Content.Blocks[0].Provenance.Extractor != "pdfium" {
		t.Errorf("content = %+v", res.Content)
	}

	resp, err := http.Get(res.Original.URL)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(original, pdf) {
		t.Errorf("signed original URL returned %d bytes, want the uploaded PDF", len(original))
	}
}

func TestTenantIsolationAndAuth(t *testing.T) {
	e := newEnv(t)
	var reg struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	body := []byte(`{"file_name": "a.pdf", "content_type": "application/pdf", "size_bytes": 10}`)
	call(t, "POST", e.docs.URL+"/v1/documents", keyA, body, nil, &reg)

	if code := call(t, "GET", e.docs.URL+"/v1/documents/"+reg.Job.ID, keyB, nil, nil, nil); code != http.StatusNotFound {
		t.Errorf("other tenant status = %d, want 404", code)
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents/"+reg.Job.ID, "", nil, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no key status = %d, want 401", code)
	}
	if code := call(t, "POST", e.docs.URL+"/v1/documents", keyA, []byte(`{"file_name": "a.pdf", "bogus": 1}`), nil, nil); code != http.StatusBadRequest {
		t.Errorf("unknown field status = %d, want 400", code)
	}
}

func TestListDocumentsAndTimeline(t *testing.T) {
	e := newEnv(t)

	var empty struct {
		Documents  []any   `json:"documents"`
		NextCursor *string `json:"next_cursor"`
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents", keyA, nil, nil, &empty); code != http.StatusOK {
		t.Fatalf("empty list status = %d", code)
	}
	if empty.Documents == nil || len(empty.Documents) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty list = %+v", empty)
	}

	first := registerUploadSubmit(t, e, keyA, "first.pdf")
	second := registerUploadSubmit(t, e, keyA, "second.pdf")
	_ = registerUploadSubmit(t, e, keyB, "other-tenant.pdf")

	var page struct {
		Documents []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			FileName string `json:"file_name"`
		} `json:"documents"`
		NextCursor *string `json:"next_cursor"`
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents?limit=1", keyA, nil, nil, &page); code != http.StatusOK {
		t.Fatalf("page status = %d", code)
	}
	if len(page.Documents) != 1 || page.Documents[0].ID != second || page.NextCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	cursor := *page.NextCursor
	if code := call(t, "GET", e.docs.URL+"/v1/documents?limit=1&cursor="+cursor, keyA, nil, nil, &page); code != http.StatusOK {
		t.Fatalf("second page status = %d", code)
	}
	if len(page.Documents) != 1 || page.Documents[0].ID != first || page.NextCursor != nil {
		t.Fatalf("second page = %+v", page)
	}

	var filtered struct {
		Documents []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"documents"`
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents?status=ready", keyA, nil, nil, &filtered); code != http.StatusOK {
		t.Fatalf("filtered status = %d", code)
	}
	if len(filtered.Documents) != 2 {
		t.Fatalf("ready docs = %+v", filtered.Documents)
	}
	for _, doc := range filtered.Documents {
		if doc.Status != "ready" {
			t.Fatalf("filtered doc = %+v", doc)
		}
	}

	var errBody map[string]string
	if code := call(t, "GET", e.docs.URL+"/v1/documents?limit=abc", keyA, nil, nil, &errBody); code != http.StatusBadRequest || errBody["error"] == "" {
		t.Fatalf("bad limit = %d %v", code, errBody)
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents?cursor=not-a-cursor", keyA, nil, nil, &errBody); code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", code)
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents?status=published", keyA, nil, nil, &errBody); code != http.StatusBadRequest {
		t.Fatalf("bad status = %d", code)
	}

	var timeline struct {
		Document struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			FileName string `json:"file_name"`
		} `json:"document"`
		Events []struct {
			ID         string `json:"id"`
			DocumentID string `json:"document_id"`
			Stage      string `json:"stage"`
			Event      string `json:"event"`
			Retry      int    `json:"retry"`
		} `json:"events"`
		Attempts []struct {
			ID         string `json:"id"`
			DocumentID string `json:"document_id"`
			Extractor  string `json:"extractor"`
			Attempt    int    `json:"attempt"`
			Status     string `json:"status"`
			CostMicros int64  `json:"cost_micros"`
		} `json:"attempts"`
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents/"+first+"/timeline", keyA, nil, nil, &timeline); code != http.StatusOK {
		t.Fatalf("timeline status = %d", code)
	}
	if timeline.Document.ID != first || timeline.Document.Status != "ready" || timeline.Document.FileName != "first.pdf" {
		t.Fatalf("timeline document = %+v", timeline.Document)
	}
	if len(timeline.Events) == 0 || timeline.Events[0].Stage != "verify" || timeline.Events[0].DocumentID != first {
		t.Fatalf("timeline events = %+v", timeline.Events)
	}
	if len(timeline.Attempts) != 1 || timeline.Attempts[0].DocumentID != first ||
		timeline.Attempts[0].Extractor != "pdfium" || timeline.Attempts[0].Attempt != 1 ||
		timeline.Attempts[0].Status != "succeeded" {
		t.Fatalf("timeline attempts = %+v", timeline.Attempts)
	}

	if code := call(t, "GET", e.docs.URL+"/v1/documents/"+first+"/timeline", keyB, nil, nil, &errBody); code != http.StatusNotFound || errBody["error"] != "not found" {
		t.Fatalf("cross-tenant timeline = %d %v", code, errBody)
	}
	if code := call(t, "GET", e.docs.URL+"/v1/documents/missing/timeline", keyA, nil, nil, &errBody); code != http.StatusNotFound {
		t.Fatalf("missing timeline = %d", code)
	}
}

func registerUploadSubmit(t *testing.T, e env, key, fileName string) string {
	t.Helper()
	var reg struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
		Upload struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		} `json:"upload"`
	}
	body := []byte(`{"file_name": "` + fileName + `", "content_type": "application/pdf", "size_bytes": ` + strconv.Itoa(len(pdf)) + `}`)
	if code := call(t, "POST", e.docs.URL+"/v1/documents", key, body, nil, &reg); code != http.StatusCreated {
		t.Fatalf("register %s status = %d", fileName, code)
	}
	if code := call(t, reg.Upload.Method, reg.Upload.URL, "", pdf, reg.Upload.Headers, nil); code >= 300 {
		t.Fatalf("upload %s status = %d", fileName, code)
	}
	if code := call(t, "POST", e.docs.URL+"/v1/documents/"+reg.Job.ID+"/submit", key, nil, nil, nil); code != http.StatusAccepted {
		t.Fatalf("submit %s status = %d", fileName, code)
	}
	return reg.Job.ID
}
