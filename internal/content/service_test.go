package content_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// publish stores a finished job with a version of the given status.
func publish(t *testing.T, store *memory.Store, blobs *localblob.Store, status document.Status) string {
	t.Helper()
	ctx := context.Background()
	j, err := document.NewJob("job-"+string(status), document.NewJobParams{
		TenantID: "tenant-a", FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: 10,
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJob(ctx, j, j.Event("e1"+j.ID(), t0)); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return j.Queue(t0) },
		func() error { return j.Claim("w", time.Minute, t0) },
		func() error { return j.RecordHash("hash", t0) },
		func() error { return j.Complete(status, "ver-"+string(status), "", t0) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	v := document.Version{
		ID: "ver-" + string(status), TenantID: "tenant-a", JobID: j.ID(), Status: status,
		ContentObject: "tenant-a/ver/content.json", OriginalObject: j.OriginalObject(), CreatedAt: t0,
	}
	raw, _ := json.Marshal(document.Content{VersionID: v.ID, Status: status})
	if err := blobs.Write(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: v.ContentObject}, "application/json", raw); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitVersion(ctx, processing.Commit{Job: j, Event: j.Event("e2"+j.ID(), t0), Version: v}); err != nil {
		t.Fatal(err)
	}
	return j.ID()
}

func TestQualityGate(t *testing.T) {
	store := memory.New()
	blobs, err := localblob.New(t.TempDir(), "http://blobs", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	svc := content.NewService(store, store, blobs, time.Minute)
	ctx := context.Background()

	partial := publish(t, store, blobs, document.StatusPartial)
	if _, err := svc.ForJob(ctx, "tenant-a", partial, content.Query{}); err != nil {
		t.Errorf("partial content err = %v, want returned", err)
	}

	review := publish(t, store, blobs, document.StatusNeedsReview)
	if _, err := svc.ForJob(ctx, "tenant-a", review, content.Query{}); !errors.Is(err, content.ErrQualityGate) {
		t.Errorf("needs_review without opt-in err = %v, want ErrQualityGate", err)
	}
	res, err := svc.ForJob(ctx, "tenant-a", review, content.Query{IncludeReview: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content.Status != document.StatusNeedsReview || res.Original.URL == "" {
		t.Errorf("result = %+v", res)
	}
	if _, err := svc.ForJob(ctx, "tenant-b", review, content.Query{IncludeReview: true}); !errors.Is(err, document.ErrNotFound) {
		t.Errorf("other tenant err = %v, want ErrNotFound", err)
	}
}
