package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/localworkflow"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/document"
)

// Ensures DELETE survives the yesbook-dev nested ServeMux mount
// (Handle("/v1/documents") + Handle("/v1/documents/")).
func TestRoutesDeleteDocument(t *testing.T) {
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

	keys, err := auth.ParseAPIKeys(defaultAPIKeys)
	if err != nil {
		t.Fatal(err)
	}
	runner := localworkflow.New(nil, log)
	t.Cleanup(runner.Close)

	srv := httptest.NewServer(routes(blobs, store, runner, keys, log))
	t.Cleanup(srv.Close)

	body := `{"file_name":"a.pdf","content_type":"application/pdf","size_bytes":13}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/documents", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer dev-key-0123456789abcdef")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var reg struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Job.ID == "" {
		t.Fatal("missing job id")
	}

	del, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/documents/"+reg.Job.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer dev-key-0123456789abcdef")
	dresp, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d allow=%q", dresp.StatusCode, dresp.Header.Get("Allow"))
	}

	if _, err := store.GetJob(context.Background(), "dev-tenant", reg.Job.ID); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("job after delete: %v", err)
	}
}
