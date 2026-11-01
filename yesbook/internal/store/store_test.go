package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avi-p/yesbook/internal/id"
	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/store"
)

func TestMemory(t *testing.T) {
	testStore(t, store.NewMemory())
}

func TestPostgres(t *testing.T) {
	url := os.Getenv("YESBOOK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set YESBOOK_TEST_DATABASE_URL to run against PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	testStore(t, store.NewPostgres(pool))
}

func testStore(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	j, err := job.New(id.New(), job.NewRequest{
		TenantID: "tenant-a", FileName: "a.pdf", ContentType: job.ContentTypePDF, SizeBytes: 10,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateJob(ctx, j, outbox.ForJob(id.New(), outbox.TypeJobRegistered, j, now)); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := s.GetJob(ctx, "tenant-a", j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != job.StatusAwaitingUpload || got.StagingObject != j.StagingObject {
		t.Errorf("GetJob = %+v, want status %s and object %s", got, job.StatusAwaitingUpload, j.StagingObject)
	}

	if _, err := s.GetJob(ctx, "tenant-b", j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetJob from other tenant: err = %v, want ErrNotFound", err)
	}

	queued := j
	queued.Status = job.StatusQueued
	ev := outbox.ForJob(id.New(), outbox.TypeJobQueued, queued, now)
	got, err = s.TransitionJob(ctx, "tenant-a", j.ID, job.StatusAwaitingUpload, job.StatusQueued, now, ev)
	if err != nil {
		t.Fatalf("TransitionJob: %v", err)
	}
	if got.Status != job.StatusQueued {
		t.Errorf("status after transition = %s, want %s", got.Status, job.StatusQueued)
	}

	ev.ID = id.New()
	_, err = s.TransitionJob(ctx, "tenant-a", j.ID, job.StatusAwaitingUpload, job.StatusQueued, now, ev)
	if !errors.Is(err, store.ErrConflict) {
		t.Errorf("repeated transition: err = %v, want ErrConflict", err)
	}

	_, err = s.TransitionJob(ctx, "tenant-a", "missing", job.StatusAwaitingUpload, job.StatusQueued, now, ev)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("transition of missing job: err = %v, want ErrNotFound", err)
	}
}
