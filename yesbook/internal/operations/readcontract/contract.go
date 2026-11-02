// Package readcontract checks that document listing and timelines behave the
// same in the memory and PostgreSQL adapters.
package readcontract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/operations"
	"github.com/avi-p/yesbook/internal/processing"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
)

// Store is the read port plus the writes a scenario needs in order to seed data.
type Store interface {
	operations.Reader
	CreateJob(ctx context.Context, j *document.Job, ev document.Event) error
	UpdateJob(ctx context.Context, j *document.Job, events ...document.Event) error
	RecordAttempt(ctx context.Context, attempt document.Attempt) error
	RecordProgress(ctx context.Context, event processing.ProgressEvent) error
}

var base = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// Run executes the shared listing and timeline scenarios against a fresh store.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	t.Run("empty list", func(t *testing.T) { testEmptyList(t, newStore(t)) })
	t.Run("orders by creation time", func(t *testing.T) { testCreationOrder(t, newStore(t)) })
	t.Run("breaks created_at ties by id", func(t *testing.T) { testIDTieBreak(t, newStore(t)) })
	t.Run("keeps a stable page when a newer document arrives", func(t *testing.T) {
		testStablePage(t, newStore(t))
	})
	t.Run("filters by status inside the tenant", func(t *testing.T) { testStatusFilter(t, newStore(t)) })
	t.Run("uses the default page size", func(t *testing.T) { testDefaultPage(t, newStore(t)) })
	t.Run("rejects invalid queries", func(t *testing.T) { testInvalidQueries(t, newStore(t)) })
	t.Run("returns the timeline", func(t *testing.T) { testTimeline(t, newStore(t)) })
	t.Run("hides another tenant timeline", func(t *testing.T) { testTimelineIsolation(t, newStore(t)) })
	t.Run("stops when the context is canceled", func(t *testing.T) { testCanceled(t, newStore(t)) })
}

func testEmptyList(t *testing.T, st Store) {
	t.Helper()
	page, err := st.ListDocuments(context.Background(), operations.ListQuery{TenantID: tenantA, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if page.Documents == nil || len(page.Documents) != 0 || page.NextCursor != "" {
		t.Fatalf("empty page = %+v", page)
	}
}

func testCreationOrder(t *testing.T, st Store) {
	t.Helper()
	older := createAt(t, st, "zzz-older", tenantA, "older.pdf", base)
	createAt(t, st, "aaa-newer", tenantA, "newer.pdf", base.Add(time.Hour))
	block(t, st, older, "encrypted pdf", base.Add(2*time.Hour))

	got := walkDocuments(t, st, operations.ListQuery{TenantID: tenantA, Limit: 1}, 1)
	if !slices.Equal(documentIDs(got), []string{"aaa-newer", "zzz-older"}) {
		t.Fatalf("documents = %v", documentIDs(got))
	}
	if got[0].Status != document.StatusAwaitingUpload || got[0].FileName != "newer.pdf" || got[0].SizeBytes != 100 {
		t.Fatalf("newer = %+v", got[0])
	}
	if !got[1].UpdatedAt.After(got[0].UpdatedAt) || got[1].Status != document.StatusBlocked || got[1].Reason != "encrypted pdf" {
		t.Fatalf("older = %+v", got[1])
	}
	if got[0].CreatedAt.Location() != time.UTC || got[1].CreatedAt.Location() != time.UTC {
		t.Fatal("created_at was not returned in UTC")
	}
}

func testIDTieBreak(t *testing.T, st Store) {
	t.Helper()
	createAt(t, st, "job-1", tenantA, "one.pdf", base)
	createAt(t, st, "job-10", tenantA, "ten.pdf", base)
	createAt(t, st, "job-2", tenantA, "two.pdf", base)

	got := walkDocuments(t, st, operations.ListQuery{TenantID: tenantA, Limit: 1}, 1)
	if !slices.Equal(documentIDs(got), []string{"job-2", "job-10", "job-1"}) {
		t.Fatalf("documents = %v", documentIDs(got))
	}
	last, err := (operations.Cursor{CreatedAt: got[len(got)-1].CreatedAt, ID: got[len(got)-1].ID}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.ListDocuments(context.Background(), operations.ListQuery{
		TenantID: tenantA, Limit: 1, Cursor: last,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Documents == nil || len(page.Documents) != 0 || page.NextCursor != "" {
		t.Fatalf("page after the last document = %+v", page)
	}
}

func testStablePage(t *testing.T, st Store) {
	t.Helper()
	createAt(t, st, "page-old", tenantA, "old.pdf", base)
	createAt(t, st, "page-mid", tenantA, "mid.pdf", base.Add(time.Minute))
	createAt(t, st, "page-new", tenantA, "new.pdf", base.Add(2*time.Minute))

	first, err := st.ListDocuments(context.Background(), operations.ListQuery{TenantID: tenantA, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Documents) != 1 || first.Documents[0].ID != "page-new" || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	createAt(t, st, "page-extra", tenantA, "extra.pdf", base.Add(3*time.Minute))

	second, err := st.ListDocuments(context.Background(), operations.ListQuery{
		TenantID: tenantA, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Documents) != 1 || second.Documents[0].ID != "page-mid" {
		t.Fatalf("second page = %+v", second.Documents)
	}
	third, err := st.ListDocuments(context.Background(), operations.ListQuery{
		TenantID: tenantA, Limit: 1, Cursor: second.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Documents) != 1 || third.Documents[0].ID != "page-old" || third.NextCursor != "" {
		t.Fatalf("third page = %+v cursor %q", third.Documents, third.NextCursor)
	}
	got := walkDocuments(t, st, operations.ListQuery{TenantID: tenantA, Limit: 10}, 10)
	if !slices.Equal(documentIDs(got), []string{"page-extra", "page-new", "page-mid", "page-old"}) {
		t.Fatalf("fresh walk = %v", documentIDs(got))
	}
}

func testStatusFilter(t *testing.T, st Store) {
	t.Helper()
	publish(t, st, createAt(t, st, "b-ready", tenantB, "b.pdf", base.Add(4*time.Hour)))
	publish(t, st, createAt(t, st, "b-old", tenantB, "b-old.pdf", base.Add(30*time.Minute)))
	publish(t, st, createAt(t, st, "ready-new", tenantA, "ready-new.pdf", base.Add(3*time.Hour)))
	queue(t, st, createAt(t, st, "queued-mid", tenantA, "queued.pdf", base.Add(2*time.Hour)))
	publish(t, st, createAt(t, st, "ready-old", tenantA, "ready-old.pdf", base.Add(time.Hour)))
	createAt(t, st, "a-old", tenantA, "a-old.pdf", base)

	ready := walkDocuments(t, st, operations.ListQuery{
		TenantID: tenantA, Status: document.StatusReady, Limit: 1,
	}, 1)
	if !slices.Equal(documentIDs(ready), []string{"ready-new", "ready-old"}) {
		t.Fatalf("ready documents = %v", documentIDs(ready))
	}
	if ready[0].FileHash != "hash-ready-new" || ready[0].VersionID != "ver-ready-new" || ready[0].FileName != "ready-new.pdf" {
		t.Fatalf("ready summary = %+v", ready[0])
	}
	queued := walkDocuments(t, st, operations.ListQuery{
		TenantID: tenantA, Status: document.StatusQueued, Limit: 10,
	}, 10)
	if !slices.Equal(documentIDs(queued), []string{"queued-mid"}) {
		t.Fatalf("queued documents = %v", documentIDs(queued))
	}
	all := walkDocuments(t, st, operations.ListQuery{TenantID: tenantA, Limit: 10}, 10)
	if !slices.Equal(documentIDs(all), []string{"ready-new", "queued-mid", "ready-old", "a-old"}) {
		t.Fatalf("tenant documents = %v", documentIDs(all))
	}

	first, err := st.ListDocuments(context.Background(), operations.ListQuery{TenantID: tenantA, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.ListDocuments(context.Background(), operations.ListQuery{
		TenantID: tenantB, Limit: 10, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(documentIDs(other.Documents), []string{"b-old"}) {
		t.Fatalf("other tenant page = %v", documentIDs(other.Documents))
	}
}

func testDefaultPage(t *testing.T, st Store) {
	t.Helper()
	count := operations.DefaultPageLimit + 1
	for i := range count {
		id := fmt.Sprintf("page-%02d", i)
		createAt(t, st, id, tenantA, id+".pdf", base.Add(time.Duration(i)*time.Second))
	}
	got := walkDocuments(t, st, operations.ListQuery{TenantID: tenantA}, operations.DefaultPageLimit)
	newest := fmt.Sprintf("page-%02d", count-1)
	if len(got) != count || got[0].ID != newest || got[len(got)-1].ID != "page-00" {
		t.Fatalf("default page = %v", documentIDs(got))
	}
}

func testInvalidQueries(t *testing.T, st Store) {
	t.Helper()
	createAt(t, st, "kept", tenantA, "kept.pdf", base)
	ctx := context.Background()
	tests := []struct {
		name  string
		query operations.ListQuery
	}{
		{name: "missing tenant", query: operations.ListQuery{Status: document.StatusReady}},
		{name: "unknown status", query: operations.ListQuery{TenantID: tenantA, Status: "published"}},
		{
			name:  "status is not interpolated",
			query: operations.ListQuery{TenantID: tenantA, Status: document.Status("ready' OR '1'='1")},
		},
		{name: "malformed cursor", query: operations.ListQuery{TenantID: tenantA, Cursor: "'; DELETE FROM jobs; --"}},
		{name: "negative limit", query: operations.ListQuery{TenantID: tenantA, Limit: -1}},
		{name: "limit above max", query: operations.ListQuery{TenantID: tenantA, Limit: operations.MaxPageLimit + 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := st.ListDocuments(ctx, tt.query); !errors.Is(err, document.ErrInvalid) {
				t.Fatalf("ListDocuments() err = %v", err)
			}
		})
	}
	if _, err := st.Timeline(ctx, "", "kept"); !errors.Is(err, document.ErrInvalid) {
		t.Fatalf("timeline without tenant err = %v", err)
	}
	if _, err := st.Timeline(ctx, tenantA, ""); !errors.Is(err, document.ErrInvalid) {
		t.Fatalf("timeline without document err = %v", err)
	}
	if _, err := st.Timeline(ctx, tenantA, "missing"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("missing timeline err = %v", err)
	}
	page, err := st.ListDocuments(ctx, operations.ListQuery{TenantID: tenantA, Limit: operations.MaxPageLimit})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(documentIDs(page.Documents), []string{"kept"}) || page.NextCursor != "" {
		t.Fatalf("documents after rejected queries = %+v", page)
	}
}

func testTimeline(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	doc := createAt(t, st, "doc-1", tenantA, "lesson.pdf", base)
	block(t, st, doc, "encrypted pdf", base.Add(time.Minute))
	createAt(t, st, "doc-2", tenantA, "other.pdf", base)
	before, err := st.Timeline(ctx, tenantA, "doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Events == nil || len(before.Events) != 0 || before.Attempts == nil || len(before.Attempts) != 0 {
		t.Fatalf("empty timeline = %+v", before)
	}

	later := base.Add(time.Hour)
	earlier := base
	if err := st.RecordProgress(ctx, progressEvent("ev-first", "doc-1", processing.ProgressStarted, nil, later)); err != nil {
		t.Fatal(err)
	}
	meta := &processing.ProgressMetadata{Current: 2, Total: 5, Unit: "pages"}
	advancing := progressEvent("ev-second", "doc-1", processing.ProgressAdvancing, meta, earlier)
	advancing.Retry = 1
	if err := st.RecordProgress(ctx, advancing); err != nil {
		t.Fatal(err)
	}
	meta.Current = 9
	failed := progressEvent("ev-third", "doc-1", processing.ProgressFailed, nil, earlier.Add(time.Second))
	failed.Stage = processing.StagePublish
	failed.Retry = 2
	failed.Reason = "checksum mismatch"
	if err := st.RecordProgress(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordProgress(ctx, progressEvent("ev-other", "doc-2", processing.ProgressStarted, nil, later)); err != nil {
		t.Fatal(err)
	}

	start := base.Add(2 * time.Minute)
	attempts := []document.Attempt{
		{
			ID: "att-b", JobID: "doc-1", TargetID: "page-1", Extractor: "docling", ExtractorVersion: "2",
			Succeeded: false, Error: "timeout", CostMicros: 15, StartedAt: start, FinishedAt: start.Add(time.Second),
		},
		{
			ID: "att-a", JobID: "doc-1", TargetID: "page-1", Extractor: "pdfium", ExtractorVersion: "1",
			Succeeded: true, CostMicros: 0, StartedAt: start, FinishedAt: start.Add(2 * time.Second),
		},
		{
			ID: "att-c", JobID: "doc-1", TargetID: "page-2", Extractor: "gemini", ExtractorVersion: "3",
			Succeeded: true, CostMicros: 40, StartedAt: start.Add(time.Minute), FinishedAt: start.Add(time.Minute + time.Second),
		},
		{
			ID: "att-other", JobID: "doc-2", TargetID: "page-1", Extractor: "docling", ExtractorVersion: "2",
			Succeeded: true, StartedAt: start, FinishedAt: start,
		},
	}
	for _, attempt := range attempts {
		if err := st.RecordAttempt(ctx, attempt); err != nil {
			t.Fatal(err)
		}
	}

	timeline, err := st.Timeline(ctx, tenantA, "doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Document.ID != "doc-1" || timeline.Document.Status != document.StatusBlocked ||
		timeline.Document.Reason != "encrypted pdf" || timeline.Document.FileName != "lesson.pdf" ||
		!timeline.Document.CreatedAt.Equal(base) || !timeline.Document.UpdatedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("document = %+v", timeline.Document)
	}
	if len(timeline.Events) != 3 || timeline.Events[0].ID != "ev-first" || timeline.Events[1].ID != "ev-second" || timeline.Events[2].ID != "ev-third" {
		t.Fatalf("events = %+v", timeline.Events)
	}
	if timeline.Events[0].Sequence <= 0 || timeline.Events[1].Sequence <= timeline.Events[0].Sequence || timeline.Events[2].Sequence <= timeline.Events[1].Sequence {
		t.Fatalf("sequences = %d %d %d", timeline.Events[0].Sequence, timeline.Events[1].Sequence, timeline.Events[2].Sequence)
	}
	if timeline.Events[1].Progress == nil || timeline.Events[1].Progress.Current != 2 || timeline.Events[1].Progress.Unit != "pages" || timeline.Events[1].Retry != 1 {
		t.Fatalf("progress = %+v", timeline.Events[1])
	}
	if timeline.Events[2].Reason != "checksum mismatch" || timeline.Events[2].Stage != processing.StagePublish || timeline.Events[2].Retry != 2 {
		t.Fatalf("failure = %+v", timeline.Events[2])
	}
	if len(timeline.Attempts) != 3 {
		t.Fatalf("attempts = %+v", timeline.Attempts)
	}
	wantIDs := []string{"att-a", "att-b", "att-c"}
	for i, id := range wantIDs {
		if timeline.Attempts[i].ID != id {
			t.Fatalf("attempt order = %s %s %s", timeline.Attempts[0].ID, timeline.Attempts[1].ID, timeline.Attempts[2].ID)
		}
	}
	first := timeline.Attempts[0]
	if !first.Succeeded || first.Extractor != "pdfium" || first.ExtractorVersion != "1" || first.CostMicros != 0 || !first.StartedAt.Equal(start) || first.StartedAt.Location() != time.UTC {
		t.Fatalf("first attempt = %+v", first)
	}
	second := timeline.Attempts[1]
	if second.Succeeded || second.Error != "timeout" || second.CostMicros != 15 || second.TargetID != "page-1" {
		t.Fatalf("failed attempt = %+v", second)
	}
	if timeline.Attempts[2].CostMicros != 40 || timeline.Attempts[2].Extractor != "gemini" {
		t.Fatalf("third attempt = %+v", timeline.Attempts[2])
	}

	timeline.Events[1].Progress.Current = 4
	timeline.Attempts[1].Error = "changed"
	again, err := st.Timeline(ctx, tenantA, "doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Events[1].Progress.Current != 2 || again.Attempts[1].Error != "timeout" {
		t.Fatalf("timeline changed after caller mutation: %+v %+v", again.Events[1].Progress, again.Attempts[1])
	}
	other, err := st.Timeline(ctx, tenantA, "doc-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Events) != 1 || other.Events[0].ID != "ev-other" || len(other.Attempts) != 1 || other.Attempts[0].ID != "att-other" {
		t.Fatalf("other document timeline = %+v", other)
	}
}

func testTimelineIsolation(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	createAt(t, st, "doc-1", tenantA, "lesson.pdf", base)
	if err := st.RecordProgress(ctx, progressEvent("ev-1", "doc-1", processing.ProgressStarted, nil, base)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAttempt(ctx, document.Attempt{
		ID: "att-1", JobID: "doc-1", TargetID: "page-1", Extractor: "pdfium", ExtractorVersion: "1",
		Succeeded: true, StartedAt: base, FinishedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Timeline(ctx, tenantB, "doc-1"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("other tenant timeline err = %v", err)
	}
	if _, err := st.Timeline(ctx, tenantA, "missing"); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("missing timeline err = %v", err)
	}
}

func testCanceled(t *testing.T, st Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.ListDocuments(ctx, operations.ListQuery{TenantID: tenantA, Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("list err = %v", err)
	}
	if _, err := st.Timeline(ctx, tenantA, "doc-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("timeline err = %v", err)
	}
}

func walkDocuments(t *testing.T, st Store, query operations.ListQuery, limit int) []operations.Document {
	t.Helper()
	const maxPages = 30
	seen := map[string]int{}
	all := make([]operations.Document, 0)
	for pageNum := range maxPages {
		page, err := st.ListDocuments(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Documents == nil {
			t.Fatal("documents slice is nil")
		}
		if page.NextCursor != "" && len(page.Documents) != limit {
			t.Fatalf("full page %d has %d documents, want %d", pageNum, len(page.Documents), limit)
		}
		if len(page.Documents) > limit {
			t.Fatalf("page %d has %d documents, limit %d", pageNum, len(page.Documents), limit)
		}
		for _, doc := range page.Documents {
			if doc.ID == "" || doc.CreatedAt.IsZero() || doc.Status == "" || doc.FileName == "" || doc.SizeBytes <= 0 {
				t.Fatalf("incomplete document %+v", doc)
			}
			seen[doc.ID]++
			if seen[doc.ID] != 1 {
				t.Fatalf("document %s returned %d times", doc.ID, seen[doc.ID])
			}
			all = append(all, doc)
		}
		if len(all) > 1 {
			prev := all[len(all)-2]
			cur := all[len(all)-1]
			if prev.CreatedAt.Before(cur.CreatedAt) || (prev.CreatedAt.Equal(cur.CreatedAt) && prev.ID < cur.ID) {
				t.Fatalf("order broke between %s and %s", prev.ID, cur.ID)
			}
		}
		if page.NextCursor == "" {
			return all
		}
		if len(page.Documents) == 0 || page.NextCursor == query.Cursor {
			t.Fatalf("cursor %q did not advance", page.NextCursor)
		}
		query.Cursor = page.NextCursor
	}
	t.Fatal("pagination did not finish")
	return nil
}

func documentIDs(docs []operations.Document) []string {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ID
	}
	return ids
}

func createAt(t *testing.T, st Store, id, tenant, fileName string, at time.Time) *document.Job {
	t.Helper()
	job, err := document.NewJob(id, document.NewJobParams{
		TenantID:    tenant,
		FileName:    fileName,
		ContentType: document.ContentTypePDF,
		SizeBytes:   100,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateJob(context.Background(), job, job.Event("ev-"+id+"-created", at)); err != nil {
		t.Fatal(err)
	}
	return job
}

func queue(t *testing.T, st Store, job *document.Job) {
	t.Helper()
	at := job.CreatedAt()
	if err := job.Queue(at); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(context.Background(), job, job.Event("ev-"+job.ID()+"-queued", at)); err != nil {
		t.Fatal(err)
	}
}

func publish(t *testing.T, st Store, job *document.Job) {
	t.Helper()
	at := job.CreatedAt()
	if err := job.Queue(at); err != nil {
		t.Fatal(err)
	}
	if err := job.Claim("worker", time.Minute, at); err != nil {
		t.Fatal(err)
	}
	if err := job.RecordHash("hash-"+job.ID(), at); err != nil {
		t.Fatal(err)
	}
	if err := job.Complete(document.StatusReady, "ver-"+job.ID(), "", at); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(context.Background(), job, job.Event("ev-"+job.ID()+"-ready", at)); err != nil {
		t.Fatal(err)
	}
}

func block(t *testing.T, st Store, job *document.Job, reason string, at time.Time) {
	t.Helper()
	if err := job.Block(reason, at); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateJob(context.Background(), job, job.Event("ev-"+job.ID()+"-blocked", at)); err != nil {
		t.Fatal(err)
	}
}

func progressEvent(
	id, jobID string,
	status processing.ProgressStatus,
	progress *processing.ProgressMetadata,
	at time.Time,
) processing.ProgressEvent {
	return processing.ProgressEvent{
		ID:         id,
		TenantID:   tenantA,
		JobID:      jobID,
		WorkflowID: "workflow-1",
		RunID:      "run-1",
		Stage:      processing.StageExtract,
		Status:     status,
		Progress:   progress,
		OccurredAt: at,
	}
}
