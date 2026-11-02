package operations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/operations"
	"github.com/avi-p/yesbook/internal/processing"
)

func TestDeriveDecisionsMarksUnsetDocling(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 2, 16, 0, 0, 0, time.UTC)
	decisions := operations.DeriveDecisions(
		[]document.Attempt{{
			Extractor: "pdfium", Decision: document.DecisionDeclined,
			ReasonCode: document.ReasonFiguresMissing, ReasonMessage: "figures missing",
			StartedAt: at,
		}},
		[]processing.ProgressEvent{{
			Status: processing.ProgressSkipped, ReasonCode: document.ReasonExtractorUnset,
			ReasonMessage: "docling url unset", OccurredAt: at,
		}, {
			Status: processing.ProgressSkipped, ReasonCode: document.ReasonChainExhausted,
			ReasonMessage: "no further extractors configured after pdfium", OccurredAt: at,
		}},
	)
	if len(decisions) != 3 {
		t.Fatalf("decisions = %d, want 3", len(decisions))
	}
	if decisions[1].Extractor != "docling" || decisions[1].Kind != document.DecisionSkipped {
		t.Fatalf("docling skip = %+v", decisions[1])
	}
	if decisions[2].ReasonCode != document.ReasonChainExhausted {
		t.Fatalf("exhausted = %+v", decisions[2])
	}
}

func TestPrepareList(t *testing.T) {
	t.Parallel()
	cursor, err := (operations.Cursor{
		CreatedAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
		ID:        "job-2",
	}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		query   operations.ListQuery
		wantErr bool
		limit   int
		afterID string
	}{
		{
			name:  "default limit",
			query: operations.ListQuery{TenantID: "tenant-a"},
			limit: operations.DefaultPageLimit,
		},
		{
			name:  "explicit limit and status",
			query: operations.ListQuery{TenantID: "tenant-a", Status: document.StatusReady, Limit: 5},
			limit: 5,
		},
		{
			name:    "cursor",
			query:   operations.ListQuery{TenantID: "tenant-a", Cursor: cursor, Limit: 1},
			limit:   1,
			afterID: "job-2",
		},
		{name: "missing tenant", query: operations.ListQuery{Status: document.StatusReady}, wantErr: true},
		{name: "unknown status", query: operations.ListQuery{TenantID: "tenant-a", Status: "published"}, wantErr: true},
		{name: "negative limit", query: operations.ListQuery{TenantID: "tenant-a", Limit: -1}, wantErr: true},
		{
			name:    "limit above max",
			query:   operations.ListQuery{TenantID: "tenant-a", Limit: operations.MaxPageLimit + 1},
			wantErr: true,
		},
		{name: "malformed cursor", query: operations.ListQuery{TenantID: "tenant-a", Cursor: "not-a-cursor"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := operations.PrepareList(tt.query)
			if tt.wantErr {
				if !errors.Is(err, document.ErrInvalid) {
					t.Fatalf("PrepareList() err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Limit != tt.limit || got.TenantID != tt.query.TenantID || got.Status != tt.query.Status {
				t.Fatalf("prepared = %+v", got)
			}
			if tt.afterID == "" && got.After != nil {
				t.Fatalf("after = %+v, want the first page", got.After)
			}
			if tt.afterID != "" && (got.After == nil || got.After.ID != tt.afterID) {
				t.Fatalf("after = %+v", got.After)
			}
		})
	}
}

func TestPageKeepsNewestOrderAndCursor(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	docs := []operations.Document{
		{ID: "job-1", CreatedAt: base},
		{ID: "job-2", CreatedAt: base},
		{ID: "job-10", CreatedAt: base},
		{ID: "aaa-newer", CreatedAt: base.Add(time.Hour)},
	}
	operations.SortDocumentsNewestFirst(docs)
	if docs[0].ID != "aaa-newer" || docs[1].ID != "job-2" || docs[2].ID != "job-10" || docs[3].ID != "job-1" {
		t.Fatalf("order = %s %s %s %s", docs[0].ID, docs[1].ID, docs[2].ID, docs[3].ID)
	}
	page, err := operations.PageOf(docs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Documents) != 2 || page.Documents[0].ID != "aaa-newer" || page.Documents[1].ID != "job-2" {
		t.Fatalf("page = %+v", page.Documents)
	}
	parsed, err := operations.ParseCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "job-2" || !parsed.CreatedAt.Equal(base) {
		t.Fatalf("cursor = %+v", parsed)
	}
	rest := []operations.Document{}
	for _, doc := range docs {
		if (operations.PageRequest{After: &parsed}).Includes(doc) {
			rest = append(rest, doc)
		}
	}
	operations.SortDocumentsNewestFirst(rest)
	if len(rest) != 2 || rest[0].ID != "job-10" || rest[1].ID != "job-1" {
		t.Fatalf("remainder = %+v", rest)
	}
}

func TestNewTimelineDetachesInputs(t *testing.T) {
	t.Parallel()
	meta := &processing.ProgressMetadata{Current: 1, Total: 2, Unit: "pages"}
	events := []processing.ProgressEvent{{ID: "ev", Progress: meta}}
	attempts := []document.Attempt{{ID: "att", Error: "timeout"}}
	timeline := operations.NewTimeline(operations.Document{ID: "doc"}, events, attempts)
	timeline.Events[0].Progress.Current = 9
	timeline.Attempts[0].Error = "changed"
	if meta.Current != 1 || attempts[0].Error != "timeout" {
		t.Fatalf("input changed: progress %d, error %q", meta.Current, attempts[0].Error)
	}
	empty := operations.NewTimeline(operations.Document{}, nil, nil)
	if empty.Events == nil || empty.Attempts == nil || len(empty.Events) != 0 || len(empty.Attempts) != 0 {
		t.Fatalf("empty timeline = %+v", empty)
	}
}

func TestValidateDocumentRef(t *testing.T) {
	t.Parallel()
	if err := operations.ValidateDocumentRef("tenant-a", "doc"); err != nil {
		t.Fatal(err)
	}
	if err := operations.ValidateDocumentRef("", "doc"); err == nil {
		t.Fatal("expected missing tenant to be invalid")
	}
	if err := operations.ValidateDocumentRef("tenant-a", ""); err == nil {
		t.Fatal("expected missing document to be invalid")
	}
}
