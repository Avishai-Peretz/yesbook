// Package storecontract checks that a repository preserves the job, version
// and outbox behavior shared by the memory and PostgreSQL adapters.
package storecontract

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/processing"
)

// Store is the repository surface both adapters implement.
type Store interface {
	CreateJob(ctx context.Context, j *document.Job, ev document.Event) error
	GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error)
	UpdateJob(ctx context.Context, j *document.Job, events ...document.Event) error
	FindCachedVersion(ctx context.Context, cacheKey string) (document.Version, bool, error)
	CommitVersion(ctx context.Context, c processing.Commit) error
	GetVersion(ctx context.Context, tenantID, versionID string) (document.Version, error)
	RecordAttempt(ctx context.Context, a document.Attempt) error
	Spend(ctx context.Context, jobID string) (document.Spend, error)
	RecordProgress(ctx context.Context, event processing.ProgressEvent) error
	ProgressEvents(ctx context.Context, tenantID, jobID string) ([]processing.ProgressEvent, error)
	ProcessPending(ctx context.Context, limit int, publish outbox.PublishFunc) (int, error)
	Backlog(ctx context.Context) (outbox.Backlog, error)
}

var epoch = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// Run executes the shared repository scenarios against a fresh store.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	t.Run("optimistic conflict", func(t *testing.T) { testConflict(t, newStore(t)) })
	t.Run("commit and tenant isolation", func(t *testing.T) { testCommit(t, newStore(t)) })
	t.Run("partial publication", func(t *testing.T) { testPartialPublish(t, newStore(t)) })
	t.Run("concurrent publishers", func(t *testing.T) { testConcurrentPublish(t, newStore(t)) })
	t.Run("publish can read the store", func(t *testing.T) { testPublishReentrancy(t, newStore(t)) })
	t.Run("processing progress", func(t *testing.T) { testProcessingProgress(t, newStore(t)) })
}

func testConflict(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	create(t, st, "job-1")
	a := mustGet(t, st, "job-1")
	b := mustGet(t, st, "job-1")
	if err := a.Queue(epoch); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(ctx, a, a.Event("ev-queued", epoch)); err != nil {
		t.Fatal(err)
	}
	if err := b.Queue(epoch); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(ctx, b); !errors.Is(err, document.ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
}

func testCommit(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	j := create(t, st, "job-2")
	if err := j.Queue(epoch); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(ctx, j, j.Event("ev-queued", epoch)); err != nil {
		t.Fatal(err)
	}
	if err := j.Claim("worker", time.Minute, epoch); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordHash("abc", epoch); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(document.StatusReady, "ver-1", "", epoch); err != nil {
		t.Fatal(err)
	}
	version := document.Version{
		ID: "ver-1", TenantID: j.TenantID(), JobID: j.ID(), FileHash: "abc",
		PipelineVersion: "p-test", Status: document.StatusReady, Coverage: 1, CreatedAt: epoch,
	}
	err := st.CommitVersion(ctx, processing.Commit{
		Job: j, Event: j.Event("ev-ready", epoch), Version: version, CacheKey: "cache-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	cached, ok, err := st.FindCachedVersion(ctx, "cache-1")
	if err != nil || !ok || cached.ID != "ver-1" {
		t.Fatalf("cache = %+v, ok=%v, err=%v", cached, ok, err)
	}
	if _, err := st.GetVersion(ctx, "other-tenant", "ver-1"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("other tenant = %v", err)
	}
	if err := st.RecordAttempt(ctx, document.Attempt{ID: "a1", JobID: j.ID(), CostMicros: 5, StartedAt: epoch}); err != nil {
		t.Fatal(err)
	}
	spend, err := st.Spend(ctx, j.ID())
	if err != nil || spend.Attempts != 1 || spend.CostMicros != 5 {
		t.Fatalf("spend = %+v, err = %v", spend, err)
	}
}

func testPartialPublish(t *testing.T, st Store) {
	t.Helper()
	create(t, st, "job-3")
	create(t, st, "job-4")
	n, err := st.ProcessPending(context.Background(), 10, func(_ context.Context, events []document.Event) ([]string, error) {
		if len(events) != 2 {
			t.Fatalf("batch = %d", len(events))
		}
		return []string{events[0].ID}, errors.New("broker down")
	})
	if n != 1 || err == nil {
		t.Fatalf("published %d, err %v", n, err)
	}
	backlog, err := st.Backlog(context.Background())
	if err != nil || backlog.Pending != 1 {
		t.Fatalf("backlog = %+v, err = %v", backlog, err)
	}
}

func testConcurrentPublish(t *testing.T, st Store) {
	t.Helper()
	create(t, st, "job-5")
	create(t, st, "job-6")
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			_, err := st.ProcessPending(context.Background(), 1, func(_ context.Context, events []document.Event) ([]string, error) {
				mu.Lock()
				defer mu.Unlock()
				ids := make([]string, len(events))
				for i, ev := range events {
					seen[ev.ID]++
					ids[i] = ev.ID
				}
				return ids, nil
			})
			if err != nil {
				t.Errorf("publish: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(seen) != 2 {
		t.Fatalf("published ids = %v", seen)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s published %d times", id, n)
		}
	}
}

func testPublishReentrancy(t *testing.T, st Store) {
	t.Helper()
	create(t, st, "job-7")
	done := make(chan struct{})
	go func() {
		_, _ = st.ProcessPending(context.Background(), 1, func(ctx context.Context, events []document.Event) ([]string, error) {
			if _, err := st.GetJob(ctx, "tenant-a", "job-7"); err != nil {
				t.Errorf("get during publish: %v", err)
			}
			close(done)
			ids := make([]string, len(events))
			for i, ev := range events {
				ids[i] = ev.ID
			}
			return ids, nil
		})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish callback could not read the store")
	}
}

func testProcessingProgress(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	create(t, st, "job-progress")

	events, err := st.ProgressEvents(ctx, "tenant-a", "job-progress")
	if err != nil || len(events) != 0 {
		t.Fatalf("empty progress = %+v, err = %v", events, err)
	}

	started := progressEvent("progress-started", processing.ProgressStarted, nil)
	if err := st.RecordProgress(ctx, started); err != nil {
		t.Fatal(err)
	}
	metadata := &processing.ProgressMetadata{Current: 2, Total: 5, Unit: "targets"}
	advancing := progressEvent("progress-advancing", processing.ProgressAdvancing, metadata)
	if err := st.RecordProgress(ctx, advancing); err != nil {
		t.Fatal(err)
	}
	metadata.Current = 5

	events, err = st.ProgressEvents(ctx, "tenant-a", "job-progress")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != started.ID || events[1].ID != advancing.ID {
		t.Fatalf("ordered progress = %+v", events)
	}
	if events[0].Sequence <= 0 || events[1].Sequence <= events[0].Sequence {
		t.Fatalf("progress sequences = %d, %d", events[0].Sequence, events[1].Sequence)
	}
	if events[1].Progress == nil || events[1].Progress.Current != 2 {
		t.Fatalf("stored progress = %+v", events[1].Progress)
	}
	events[1].Progress.Current = 4
	again, err := st.ProgressEvents(ctx, "tenant-a", "job-progress")
	if err != nil || again[1].Progress.Current != 2 {
		t.Fatalf("progress after caller mutation = %+v, err = %v", again, err)
	}

	if _, err := st.ProgressEvents(ctx, "tenant-b", "job-progress"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("other tenant read = %v", err)
	}
	wrongTenant := progressEvent("wrong-tenant", processing.ProgressStarted, nil)
	wrongTenant.TenantID = "tenant-b"
	if err := st.RecordProgress(ctx, wrongTenant); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("other tenant write = %v", err)
	}
	invalid := progressEvent("invalid", processing.ProgressAdvancing, nil)
	if err := st.RecordProgress(ctx, invalid); !errors.Is(err, processing.ErrInvalidProgressEvent) {
		t.Fatalf("invalid progress = %v", err)
	}
}

func progressEvent(
	id string,
	status processing.ProgressStatus,
	progress *processing.ProgressMetadata,
) processing.ProgressEvent {
	return processing.ProgressEvent{
		ID:         id,
		TenantID:   "tenant-a",
		JobID:      "job-progress",
		WorkflowID: "workflow-1",
		RunID:      "run-1",
		Retry:      1,
		Stage:      processing.StageExtract,
		Status:     status,
		Progress:   progress,
		OccurredAt: epoch,
	}
}

func create(t *testing.T, st Store, id string) *document.Job {
	t.Helper()
	j, err := document.NewJob(id, document.NewJobParams{
		TenantID: "tenant-a", FileName: "lesson.pdf", ContentType: document.ContentTypePDF, SizeBytes: 100,
	}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateJob(context.Background(), j, j.Event("ev-"+id, epoch)); err != nil {
		t.Fatal(err)
	}
	return j
}

func mustGet(t *testing.T, st Store, id string) *document.Job {
	t.Helper()
	j, err := st.GetJob(context.Background(), "tenant-a", id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
