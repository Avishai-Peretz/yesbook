package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/avi-p/yesbook/internal/api"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/blob"
	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/store"
)

type recordingStarter struct {
	mu     sync.Mutex
	starts []string
}

func (s *recordingStarter) Start(_ context.Context, _, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, jobID)
	return nil
}

type env struct {
	srv     *httptest.Server
	store   *store.Memory
	starter *recordingStarter
}

func newEnv(t *testing.T) *env {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	blobs, err := blob.NewLocal(t.TempDir(), srv.URL, bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	authn, err := auth.ParseStaticKeys("key-a:tenant-a,key-b:tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{srv: srv, store: store.NewMemory(), starter: &recordingStarter{}}
	api.New(api.Config{
		Store:     e.store,
		Blobs:     blobs,
		Auth:      authn,
		Workflows: e.starter,
		Logger:    slog.New(slog.DiscardHandler),
	}).Register(mux)
	mux.HandleFunc("PUT /uploads/{object...}", blobs.HandleUpload)
	return e
}

func (e *env) do(t *testing.T, method, path, apiKey string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return send(t, req)
}

func send(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

type created struct {
	Job       job.Job `json:"job"`
	UploadURL string  `json:"upload_url"`
}

func (e *env) create(t *testing.T, apiKey string, size int) created {
	t.Helper()
	resp, body := e.do(t, http.MethodPost, "/v1/documents", apiKey, map[string]any{
		"file_name": "lesson.pdf", "content_type": job.ContentTypePDF, "size_bytes": size,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", resp.StatusCode, body)
	}
	var c created
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func upload(t *testing.T, uploadURL string, data []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", job.ContentTypePDF)
	resp, _ := send(t, req)
	return resp
}

func TestUploadAndSubmit(t *testing.T) {
	e := newEnv(t)
	pdf := []byte("%PDF-1.7 test document")

	c := e.create(t, "key-a", len(pdf))
	if c.Job.Status != job.StatusAwaitingUpload {
		t.Fatalf("status = %s, want %s", c.Job.Status, job.StatusAwaitingUpload)
	}

	if resp := upload(t, c.UploadURL, pdf); resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d", resp.StatusCode)
	}

	resp, body := e.do(t, http.MethodPost, "/v1/documents/"+c.Job.ID+"/submit", "key-a", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit: status %d, body %s", resp.StatusCode, body)
	}

	resp, body = e.do(t, http.MethodGet, "/v1/documents/"+c.Job.ID, "key-a", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"queued"`) {
		t.Fatalf("get: status %d, body %s", resp.StatusCode, body)
	}

	var types []string
	for _, ev := range e.store.Events() {
		types = append(types, ev.Type)
	}
	if want := []string{outbox.TypeJobRegistered, outbox.TypeJobQueued}; strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("outbox events = %v, want %v", types, want)
	}
	if len(e.starter.starts) != 1 || e.starter.starts[0] != c.Job.ID {
		t.Errorf("workflow starts = %v, want [%s]", e.starter.starts, c.Job.ID)
	}
}

func TestSubmitBeforeUpload(t *testing.T) {
	e := newEnv(t)
	c := e.create(t, "key-a", 10)
	resp, _ := e.do(t, http.MethodPost, "/v1/documents/"+c.Job.ID+"/submit", "key-a", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

func TestTenantIsolation(t *testing.T) {
	e := newEnv(t)
	c := e.create(t, "key-a", 10)
	resp, _ := e.do(t, http.MethodGet, "/v1/documents/"+c.Job.ID, "key-b", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestRejectsUnauthenticated(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, http.MethodPost, "/v1/documents", "wrong-key", map[string]any{})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestRejectsNonPDF(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, http.MethodPost, "/v1/documents", "key-a", map[string]any{
		"file_name": "notes.docx", "content_type": "application/msword", "size_bytes": 10,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestUploadRejectsTamperedURL(t *testing.T) {
	e := newEnv(t)
	c := e.create(t, "key-a", 10)
	tampered := strings.Replace(c.UploadURL, "max=10", "max=999999", 1)
	if resp := upload(t, tampered, make([]byte, 20)); resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestUploadRejectsOversizedBody(t *testing.T) {
	e := newEnv(t)
	c := e.create(t, "key-a", 10)
	if resp := upload(t, c.UploadURL, make([]byte, 20)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}
