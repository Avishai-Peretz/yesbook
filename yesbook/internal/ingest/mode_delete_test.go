package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

type noopWorkflow struct{}

func (noopWorkflow) Start(context.Context, string, string) error { return nil }

func TestRegisterRejectsUnknownAndUnconfiguredMode(t *testing.T) {
	store := memory.New()
	blobs := testBlobs(t)
	svc := ingest.NewService(store, blobs, noopWorkflow{}, ingest.WithConfiguredModes(map[string]bool{
		"pdfium": true,
	}))
	ctx := context.Background()

	_, err := svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: 10,
		ExtractionMode: "nope",
	})
	if !errors.Is(err, document.ErrInvalid) {
		t.Fatalf("unknown mode err = %v", err)
	}

	_, err = svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: 10,
		ExtractionMode: document.ModeDocling,
	})
	if !errors.Is(err, document.ErrInvalid) {
		t.Fatalf("unconfigured mode err = %v", err)
	}

	reg, err := svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: 10,
		ExtractionMode: document.ModePDFium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reg.Job.ExtractionMode() != document.ModePDFium {
		t.Fatalf("mode = %s", reg.Job.ExtractionMode())
	}
}

func TestDeleteRemovesJobAndSharedVersionWhenLastRef(t *testing.T) {
	store := memory.New()
	blobs := testBlobs(t)
	svc := ingest.NewService(store, blobs, noopWorkflow{})
	ctx := context.Background()

	reg, err := svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: int64(len(pdfBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	putSigned(t, reg.Upload, pdfBody)

	j, err := svc.Submit(ctx, "tenant-a", reg.Job.ID(), ingest.SubmitRequest{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if err := j.Claim("w", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordHash("hash-a", now); err != nil {
		t.Fatal(err)
	}
	if err := blobs.Write(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: j.OriginalObject()}, "application/pdf", pdfBody); err != nil {
		t.Fatal(err)
	}
	contentName := "tenant-a/ver-1/content.json"
	if err := blobs.Write(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: contentName}, "application/json", []byte(`{"blocks":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(document.StatusReady, "ver-1", "", now); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitVersion(ctx, processing.Commit{
		Job:   j,
		Event: j.Event("ev-ready", now),
		Version: document.Version{
			ID: "ver-1", TenantID: "tenant-a", JobID: j.ID(), FileHash: "hash-a",
			Status: document.StatusReady, ContentObject: contentName, OriginalObject: j.OriginalObject(), CreatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(ctx, "tenant-a", j.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetJob(ctx, "tenant-a", j.ID()); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("job after delete: %v", err)
	}
	if _, err := store.GetVersion(ctx, "tenant-a", "ver-1"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("version after delete: %v", err)
	}
	if _, err := blobs.Stat(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: j.OriginalObject()}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("original after delete: %v", err)
	}
	if _, err := blobs.Stat(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: contentName}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("content after delete: %v", err)
	}
}

func TestDeleteReassignsSharedVersionOwner(t *testing.T) {
	store := memory.New()
	blobs := testBlobs(t)
	svc := ingest.NewService(store, blobs, noopWorkflow{})
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	owner := readyJobWithVersion(t, ctx, svc, store, blobs, "owner.pdf", "ver-shared", now)
	later := now.Add(time.Minute)
	dup, err := svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: "dup.pdf", ContentType: document.ContentTypePDF, SizeBytes: int64(len(pdfBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	putSigned(t, dup.Upload, pdfBody)
	dj, err := svc.Submit(ctx, "tenant-a", dup.Job.ID(), ingest.SubmitRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dj.Claim("w", time.Minute, later); err != nil {
		t.Fatal(err)
	}
	if err := dj.RecordHash("hash-a", later); err != nil {
		t.Fatal(err)
	}
	if err := dj.Complete(document.StatusReady, "ver-shared", "", later); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJob(ctx, dj, dj.Event("ev-dup-ready", later)); err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(ctx, "tenant-a", owner.ID()); err != nil {
		t.Fatal(err)
	}
	v, err := store.GetVersion(ctx, "tenant-a", "ver-shared")
	if err != nil {
		t.Fatal(err)
	}
	if v.JobID != dj.ID() {
		t.Fatalf("version job_id = %q, want surviving job %q", v.JobID, dj.ID())
	}
	if _, err := store.GetJob(ctx, "tenant-a", dj.ID()); err != nil {
		t.Fatalf("surviving job: %v", err)
	}
}

func readyJobWithVersion(
	t *testing.T, ctx context.Context, svc *ingest.Service, store *memory.Store, blobs *localblob.Store,
	fileName, versionID string, now time.Time,
) *document.Job {
	t.Helper()
	reg, err := svc.Register(ctx, "tenant-a", ingest.RegisterRequest{
		FileName: fileName, ContentType: document.ContentTypePDF, SizeBytes: int64(len(pdfBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	putSigned(t, reg.Upload, pdfBody)
	j, err := svc.Submit(ctx, "tenant-a", reg.Job.ID(), ingest.SubmitRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Claim("w", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordHash("hash-a", now); err != nil {
		t.Fatal(err)
	}
	if err := blobs.Write(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: j.OriginalObject()}, "application/pdf", pdfBody); err != nil {
		t.Fatal(err)
	}
	contentName := "tenant-a/" + versionID + "/content.json"
	if err := blobs.Write(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: contentName}, "application/json", []byte(`{"blocks":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(document.StatusReady, versionID, "", now); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitVersion(ctx, processing.Commit{
		Job:   j,
		Event: j.Event("ev-ready-"+versionID, now),
		Version: document.Version{
			ID: versionID, TenantID: "tenant-a", JobID: j.ID(), FileHash: "hash-a",
			Status: document.StatusReady, ContentObject: contentName, OriginalObject: j.OriginalObject(), CreatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return j
}

var pdfBody = []byte("%PDF-1.7 tiny")

func testBlobs(t *testing.T) *localblob.Store {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	blobs, err := localblob.New(t.TempDir(), "http://"+srv.Listener.Addr().String(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = blobs.Handler()
	srv.Start()
	t.Cleanup(srv.Close)
	return blobs
}

func putSigned(t *testing.T, upload storage.SignedURL, body []byte) {
	t.Helper()
	req, err := http.NewRequest(upload.Method, upload.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range upload.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
}
