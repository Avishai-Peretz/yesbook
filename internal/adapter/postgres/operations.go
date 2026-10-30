package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/operations"
)

var _ operations.Reader = (*Store)(nil)

// Keyset pagination for ORDER BY created_at DESC, id COLLATE "C" DESC.
// The cursor is a position, not an authorization token; tenant_id still filters every row.
const listDocumentsSQL = `
SELECT ` + jobColumns + `
  FROM jobs
 WHERE tenant_id = $1
   AND ($2 = '' OR status = $2)
   AND (
        $3::timestamptz IS NULL
        OR created_at < $3
        OR (created_at = $3 AND id COLLATE "C" < $4)
       )
 ORDER BY created_at DESC, id COLLATE "C" DESC
 LIMIT $5`

const listAttemptsSQL = `
SELECT a.id, a.job_id, a.target_id, a.extractor, a.extractor_version,
       a.succeeded, a.error, a.cost_micros, a.started_at, a.finished_at,
       a.reason_code, a.reason_message, a.decision
  FROM attempts a
  JOIN jobs j ON j.id = a.job_id
 WHERE a.job_id = $1
   AND j.tenant_id = $2
 ORDER BY a.started_at ASC, a.id COLLATE "C" ASC`

// ListDocuments returns one newest-first page of the tenant's documents.
func (s *Store) ListDocuments(ctx context.Context, query operations.ListQuery) (operations.DocumentPage, error) {
	if err := ctx.Err(); err != nil {
		return operations.DocumentPage{}, fmt.Errorf("list documents: %w", err)
	}
	req, err := operations.PrepareList(query)
	if err != nil {
		return operations.DocumentPage{}, err
	}
	var afterAt *time.Time
	var afterID *string
	if req.After != nil {
		at := req.After.CreatedAt.UTC()
		id := req.After.ID
		afterAt = &at
		afterID = &id
	}
	rows, err := s.pool.Query(
		ctx,
		listDocumentsSQL,
		req.TenantID,
		string(req.Status),
		afterAt,
		afterID,
		req.Limit+1,
	)
	if err != nil {
		return operations.DocumentPage{}, fmt.Errorf("query documents: %w", err)
	}
	docs, err := pgx.CollectRows(rows, scanDocument)
	if err != nil {
		return operations.DocumentPage{}, fmt.Errorf("read documents: %w", err)
	}
	return operations.PageOf(docs, req.Limit)
}

func scanDocument(row pgx.CollectableRow) (operations.Document, error) {
	var record document.JobRecord
	if err := row.Scan(
		&record.ID,
		&record.TenantID,
		&record.FileName,
		&record.SizeBytes,
		&record.Status,
		&record.FileHash,
		&record.VersionID,
		&record.Reason,
		&record.ExtractionMode,
		&record.ClaimedBy,
		&record.Revision,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return operations.Document{}, err
	}
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	return operations.NewDocument(document.RestoreJob(record)), nil
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
	attempts, err := s.listAttempts(ctx, tenantID, documentID)
	if err != nil {
		return operations.Timeline{}, err
	}
	return operations.NewTimeline(operations.NewDocument(job), events, attempts), nil
}

func (s *Store) listAttempts(ctx context.Context, tenantID, jobID string) ([]document.Attempt, error) {
	rows, err := s.pool.Query(ctx, listAttemptsSQL, jobID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query attempts: %w", err)
	}
	attempts, err := pgx.CollectRows(rows, scanAttempt)
	if err != nil {
		return nil, fmt.Errorf("read attempts: %w", err)
	}
	if attempts == nil {
		return []document.Attempt{}, nil
	}
	return attempts, nil
}

func scanAttempt(row pgx.CollectableRow) (document.Attempt, error) {
	var attempt document.Attempt
	if err := row.Scan(
		&attempt.ID,
		&attempt.JobID,
		&attempt.TargetID,
		&attempt.Extractor,
		&attempt.ExtractorVersion,
		&attempt.Succeeded,
		&attempt.Error,
		&attempt.CostMicros,
		&attempt.StartedAt,
		&attempt.FinishedAt,
		&attempt.ReasonCode,
		&attempt.ReasonMessage,
		&attempt.Decision,
	); err != nil {
		return document.Attempt{}, err
	}
	attempt.StartedAt = attempt.StartedAt.UTC()
	attempt.FinishedAt = attempt.FinishedAt.UTC()
	return attempt, nil
}
