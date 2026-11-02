package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/adapter/storecontract"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

// Set YESBOOK_TEST_DATABASE_URL to a disposable database to run these tests;
// they truncate every table. docker compose creates one:
// postgres://yesbook:yesbook@localhost:5433/yesbook_test?sslmode=disable.
func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	url := os.Getenv("YESBOOK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("YESBOOK_TEST_DATABASE_URL is not set")
	}
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE processing_events, outbox, quality_results, cache_keys, attempts, versions, jobs`); err != nil {
		t.Fatal(err)
	}
	return postgres.New(pool)
}

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func createJob(t *testing.T, s *postgres.Store, id string) *document.Job {
	t.Helper()
	j, err := document.NewJob(id, document.NewJobParams{
		TenantID: "tenant-a", FileName: "lesson.pdf", ContentType: document.ContentTypePDF, SizeBytes: 100,
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateJob(context.Background(), j, j.Event("ev-"+id+"-created", t0)); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJobOptimisticConcurrency(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	createJob(t, s, "job-1")

	a, err := s.GetJob(ctx, "tenant-a", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.GetJob(ctx, "tenant-a", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Queue(t0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateJob(ctx, a, a.Event("ev-queued", t0)); err != nil {
		t.Fatal(err)
	}
	if err := b.Queue(t0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateJob(ctx, b); !errors.Is(err, document.ErrConflict) {
		t.Errorf("stale update err = %v, want ErrConflict", err)
	}
	if _, err := s.GetJob(ctx, "tenant-b", "job-1"); !errors.Is(err, document.ErrNotFound) {
		t.Errorf("other tenant err = %v, want ErrNotFound", err)
	}
}

func TestCommitVersionAndCache(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	j := createJob(t, s, "job-1")
	for _, step := range []func() error{
		func() error { return j.Queue(t0) },
		func() error { return j.Claim("w", time.Minute, t0) },
		func() error { return j.RecordHash("abc", t0) },
		func() error { return j.Complete(document.StatusReady, "ver-1", "", t0) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	c := processing.Commit{
		Job:   j,
		Event: j.Event("ev-ready", t0),
		Version: document.Version{
			ID: "ver-1", TenantID: "tenant-a", JobID: "job-1", FileHash: "abc", PipelineVersion: "p-1",
			Status: document.StatusReady, Coverage: 1, ContentObject: "c.json", OriginalObject: "o.pdf", CreatedAt: t0,
		},
		CacheKey: "key-1",
		Quality: []document.QualityResult{{
			TargetID: "p0001", Extractor: "pdfium", ExtractorVersion: "1", Verdict: document.VerdictAccept,
			Checks: []document.Check{{Name: "coverage", Passed: true, Score: 1}},
		}},
	}
	if err := s.CommitVersion(ctx, c); err != nil {
		t.Fatal(err)
	}

	v, ok, err := s.FindCachedVersion(ctx, "key-1")
	if err != nil || !ok || v.ID != "ver-1" || v.Status != document.StatusReady {
		t.Fatalf("cached = %+v, %v, %v", v, ok, err)
	}
	got, err := s.GetJob(ctx, "tenant-a", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status() != document.StatusReady || got.VersionID() != "ver-1" {
		t.Errorf("job = %+v", got.Record())
	}
	if _, err := s.GetVersion(ctx, "tenant-b", "ver-1"); !errors.Is(err, document.ErrNotFound) {
		t.Errorf("other tenant err = %v, want ErrNotFound", err)
	}
}

func TestSpendAccumulates(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	createJob(t, s, "job-1")
	for i, cost := range []int64{100, 250} {
		a := document.Attempt{
			ID: string(rune('a' + i)), JobID: "job-1", TargetID: "p0001", Extractor: "gemini", ExtractorVersion: "1",
			Succeeded: true, CostMicros: cost, StartedAt: t0.Add(time.Duration(i) * time.Minute), FinishedAt: t0,
		}
		if err := s.RecordAttempt(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	sp, err := s.Spend(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Attempts != 2 || sp.CostMicros != 350 || !sp.FirstAttemptAt.Equal(t0) {
		t.Errorf("spend = %+v", sp)
	}
}

func TestOutboxProcessPending(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	createJob(t, s, "job-1")
	createJob(t, s, "job-2")

	n, err := s.ProcessPending(ctx, 10, func(_ context.Context, events []document.Event) ([]string, error) {
		if len(events) != 2 || events[0].JobID != "job-1" {
			t.Errorf("events = %+v", events)
		}
		return []string{events[0].ID}, errors.New("second publish failed")
	})
	if n != 1 || err == nil {
		t.Fatalf("n = %d, err = %v; want 1 and the publish error", n, err)
	}
	b, err := s.Backlog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Pending != 1 {
		t.Errorf("pending = %d, want 1", b.Pending)
	}
}

func TestListenWakesOnCommit(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		s.Wait()
	}()
	wake := s.Listen(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	time.Sleep(200 * time.Millisecond) // let LISTEN register

	createJob(t, s, "job-1")
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("no notification after commit")
	}
}

func TestRepositoryContract(t *testing.T) {
	storecontract.Run(t, func(t *testing.T) storecontract.Store { return newStore(t) })
}
