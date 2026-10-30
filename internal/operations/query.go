// Package operations serves tenant-scoped reads for document history:
// cursor-paginated listings and per-document timelines.
package operations

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

const (
	// DefaultPageLimit is the page size used when a list query omits a limit.
	DefaultPageLimit = 20
	// MaxPageLimit is the largest page a list query may request.
	MaxPageLimit = 100
)

// Reader is the read-side persistence port. Adapters implement it with the
// same pagination, ordering, and tenant rules.
type Reader interface {
	ListDocuments(ctx context.Context, query ListQuery) (DocumentPage, error)
	Timeline(ctx context.Context, tenantID, documentID string) (Timeline, error)
}

// ListQuery selects one page of a tenant's documents. An empty cursor reads
// the first page. Limit 0 selects DefaultPageLimit.
type ListQuery struct {
	TenantID string
	Status   document.Status
	Cursor   string
	Limit    int
}

// Document is the listing and timeline summary of one job. It carries the
// fields a caller can show without the worker lease or revision.
type Document struct {
	ID             string
	Status         document.Status
	FileName       string
	SizeBytes      int64
	FileHash       string
	VersionID      string
	Reason         string
	ExtractionMode document.ExtractionMode
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DocumentPage is one newest-first page. NextCursor is empty on the last page.
type DocumentPage struct {
	Documents  []Document
	NextCursor string
}

// Timeline is one document plus its progress events and extractor attempts.
// Events follow the persistence sequence. Attempts follow started_at, then id.
// Decisions are derived from attempts and skipped progress reasons.
type Timeline struct {
	Document  Document
	Events    []processing.ProgressEvent
	Attempts  []document.Attempt
	Decisions []document.PathDecision
}

// PageRequest is a validated list query. After is nil on the first page.
// Ordering is created_at descending, then id descending, so a later status
// change does not move a document inside an in-progress walk.
type PageRequest struct {
	TenantID string
	Status   document.Status
	Limit    int
	After    *Cursor
}

// NewDocument copies the public summary of j. j must be non-nil.
func NewDocument(j *document.Job) Document {
	return Document{
		ID:             j.ID(),
		Status:         j.Status(),
		FileName:       j.FileName(),
		SizeBytes:      j.SizeBytes(),
		FileHash:       j.FileHash(),
		VersionID:      j.VersionID(),
		Reason:         j.Reason(),
		ExtractionMode: j.ExtractionMode(),
		CreatedAt:      j.CreatedAt().UTC(),
		UpdatedAt:      j.UpdatedAt().UTC(),
	}
}

// NewTimeline returns a timeline whose slices and progress metadata are
// detached from the caller's inputs. Empty inputs become empty slices.
func NewTimeline(
	doc Document,
	events []processing.ProgressEvent,
	attempts []document.Attempt,
) Timeline {
	clonedEvents := cloneEvents(events)
	clonedAttempts := cloneAttempts(attempts)
	return Timeline{
		Document:  doc,
		Events:    clonedEvents,
		Attempts:  clonedAttempts,
		Decisions: DeriveDecisions(clonedAttempts, clonedEvents),
	}
}

// DeriveDecisions builds path decisions from attempts and coded progress events.
func DeriveDecisions(attempts []document.Attempt, events []processing.ProgressEvent) []document.PathDecision {
	out := make([]document.PathDecision, 0)
	for _, a := range attempts {
		if a.Decision == "" && a.ReasonCode == "" {
			continue
		}
		kind := a.Decision
		if kind == "" {
			kind = document.DecisionChosen
		}
		msg := a.ReasonMessage
		if msg == "" {
			msg = a.Error
		}
		if msg == "" {
			msg = a.ReasonCode
		}
		out = append(out, document.PathDecision{
			Kind:          kind,
			ReasonCode:    a.ReasonCode,
			ReasonMessage: msg,
			Extractor:     a.Extractor,
			TargetID:      a.TargetID,
			OccurredAt:    a.StartedAt.UTC(),
		})
	}
	for _, e := range events {
		if e.ReasonCode == "" {
			continue
		}
		if e.Status != processing.ProgressSkipped && e.Status != processing.ProgressCompleted {
			continue
		}
		msg := e.ReasonMessage
		if msg == "" {
			msg = e.Reason
		}
		kind := document.DecisionSkipped
		if e.Status == processing.ProgressCompleted {
			kind = document.DecisionChosen
		}
		pd := document.PathDecision{
			Kind:          kind,
			ReasonCode:    e.ReasonCode,
			ReasonMessage: msg,
			OccurredAt:    e.OccurredAt.UTC(),
		}
		// Progress events have no extractor field; infer Docling skips so the
		// timeline can show declined PDFium → skipped Docling → next tool.
		if e.ReasonCode == document.ReasonExtractorUnset {
			pd.Extractor = string(document.ModeDocling)
		}
		out = append(out, pd)
	}
	return out
}

// ValidateDocumentRef reports whether a timeline lookup has both identifiers.
func ValidateDocumentRef(tenantID, documentID string) error {
	if tenantID == "" || documentID == "" {
		return fmt.Errorf("%w: tenant and document are required", document.ErrInvalid)
	}
	return nil
}

// PrepareList validates q and resolves its page size and cursor.
func PrepareList(q ListQuery) (PageRequest, error) {
	if q.TenantID == "" {
		return PageRequest{}, fmt.Errorf("%w: tenant is required", document.ErrInvalid)
	}
	if !knownStatus(q.Status) {
		return PageRequest{}, fmt.Errorf("%w: unknown status", document.ErrInvalid)
	}
	limit := q.Limit
	if limit == 0 {
		limit = DefaultPageLimit
	}
	if limit < 1 || limit > MaxPageLimit {
		return PageRequest{}, fmt.Errorf("%w: limit must be between 1 and %d", document.ErrInvalid, MaxPageLimit)
	}
	req := PageRequest{TenantID: q.TenantID, Status: q.Status, Limit: limit}
	if q.Cursor == "" {
		return req, nil
	}
	cursor, err := ParseCursor(q.Cursor)
	if err != nil {
		return PageRequest{}, err
	}
	req.After = &cursor
	return req, nil
}

// Includes reports whether doc belongs on this page. The caller already
// restricted documents to the requested tenant.
func (r PageRequest) Includes(doc Document) bool {
	if r.Status != "" && doc.Status != r.Status {
		return false
	}
	if r.After == nil {
		return true
	}
	if doc.CreatedAt.Equal(r.After.CreatedAt) {
		return doc.ID < r.After.ID
	}
	return doc.CreatedAt.Before(r.After.CreatedAt)
}

// SortDocumentsNewestFirst orders documents by created_at descending and id
// descending. The id comparison is bytewise, matching PostgreSQL COLLATE "C".
func SortDocumentsNewestFirst(docs []Document) {
	slices.SortFunc(docs, func(a, b Document) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
}

// PageOf keeps the first limit documents of a newest-first slice. When more
// rows were supplied, NextCursor addresses the last returned document.
func PageOf(newestFirst []Document, limit int) (DocumentPage, error) {
	if limit < 1 {
		return DocumentPage{}, fmt.Errorf("%w: limit must be positive", document.ErrInvalid)
	}
	if len(newestFirst) > limit {
		next, err := (Cursor{
			CreatedAt: newestFirst[limit-1].CreatedAt,
			ID:        newestFirst[limit-1].ID,
		}).Encode()
		if err != nil {
			return DocumentPage{}, err
		}
		page := make([]Document, limit)
		copy(page, newestFirst[:limit])
		return DocumentPage{Documents: page, NextCursor: next}, nil
	}
	page := make([]Document, len(newestFirst))
	copy(page, newestFirst)
	return DocumentPage{Documents: page}, nil
}

func knownStatus(status document.Status) bool {
	switch status {
	case "",
		document.StatusAwaitingUpload,
		document.StatusQueued,
		document.StatusProcessing,
		document.StatusReady,
		document.StatusPartial,
		document.StatusNeedsReview,
		document.StatusBlocked:
		return true
	default:
		return false
	}
}

func cloneEvents(events []processing.ProgressEvent) []processing.ProgressEvent {
	out := make([]processing.ProgressEvent, len(events))
	for i, event := range events {
		if event.Progress != nil {
			progress := *event.Progress
			event.Progress = &progress
		}
		out[i] = event
	}
	return out
}

func cloneAttempts(attempts []document.Attempt) []document.Attempt {
	out := make([]document.Attempt, len(attempts))
	copy(out, attempts)
	return out
}
