package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/operations"
)

var _ operations.Reader = (*Store)(nil)

// ListDocuments returns one newest-first page of the tenant's documents.
func (s *Store) ListDocuments(ctx context.Context, query operations.ListQuery) (operations.DocumentPage, error) {
	if err := ctx.Err(); err != nil {
		return operations.DocumentPage{}, fmt.Errorf("list documents: %w", err)
	}
	req, err := operations.PrepareList(query)
	if err != nil {
		return operations.DocumentPage{}, err
	}
	docs := s.matchDocuments(req)
	operations.SortDocumentsNewestFirst(docs)
	return operations.PageOf(docs, req.Limit)
}

func (s *Store) matchDocuments(req operations.PageRequest) []operations.Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	docs := make([]operations.Document, 0)
	for _, record := range s.jobs {
		if record.TenantID != req.TenantID {
			continue
		}
		doc := operations.NewDocument(document.RestoreJob(record))
		if !req.Includes(doc) {
			continue
		}
		docs = append(docs, doc)
	}
	return docs
}

// Timeline returns the tenant's document, its progress events in sequence
// order, and its attempts ordered by start time.
func (s *Store) Timeline(ctx context.Context, tenantID, documentID string) (operations.Timeline, error) {
	if err := ctx.Err(); err != nil {
		return operations.Timeline{}, fmt.Errorf("read timeline: %w", err)
	}
	if err := operations.ValidateDocumentRef(tenantID, documentID); err != nil {
		return operations.Timeline{}, err
	}
	job, err := s.GetJob(ctx, tenantID, documentID)
	if err != nil {
		return operations.Timeline{}, err
	}
	events, err := s.ProgressEvents(ctx, tenantID, documentID)
	if err != nil {
		return operations.Timeline{}, err
	}
	return operations.NewTimeline(operations.NewDocument(job), events, s.attemptsFor(documentID)), nil
}

func (s *Store) attemptsFor(jobID string) []document.Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := make([]document.Attempt, 0)
	for _, attempt := range s.attempts {
		if attempt.JobID != jobID {
			continue
		}
		attempt.StartedAt = attempt.StartedAt.UTC()
		attempt.FinishedAt = attempt.FinishedAt.UTC()
		attempts = append(attempts, attempt)
	}
	slices.SortFunc(attempts, func(a, b document.Attempt) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return attempts
}
