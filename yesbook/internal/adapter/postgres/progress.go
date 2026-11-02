package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

var _ processing.ProgressSink = (*Store)(nil)

func (s *Store) RecordProgress(ctx context.Context, event processing.ProgressEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	var current, total, unit any
	if event.Progress != nil {
		current = event.Progress.Current
		total = event.Progress.Total
		unit = event.Progress.Unit
	}
	reasonMessage := event.ReasonMessage
	if reasonMessage == "" {
		reasonMessage = event.Reason
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO processing_events (
			id, tenant_id, job_id, workflow_id, run_id, retry, stage, status,
			progress_current, progress_total, progress_unit, reason, reason_code, occurred_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		FROM jobs WHERE id = $3 AND tenant_id = $2`,
		event.ID, event.TenantID, event.JobID, event.WorkflowID, event.RunID,
		event.Retry, event.Stage, event.Status, current, total, unit, reasonMessage, event.ReasonCode, event.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert processing event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return document.ErrNotFound
	}
	return nil
}

func (s *Store) ProgressEvents(
	ctx context.Context,
	tenantID, jobID string,
) ([]processing.ProgressEvent, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1 AND tenant_id = $2)`,
		jobID, tenantID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check progress job: %w", err)
	}
	if !exists {
		return nil, document.ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`SELECT seq, id, tenant_id, job_id, workflow_id, run_id, retry, stage, status,
		        progress_current, progress_total, progress_unit, reason, reason_code, occurred_at
		   FROM processing_events
		  WHERE tenant_id = $1 AND job_id = $2
		  ORDER BY seq`,
		tenantID, jobID)
	if err != nil {
		return nil, fmt.Errorf("query processing events: %w", err)
	}
	events, err := pgx.CollectRows(rows, scanProgressEvent)
	if err != nil {
		return nil, fmt.Errorf("read processing events: %w", err)
	}
	return events, nil
}

func scanProgressEvent(row pgx.CollectableRow) (processing.ProgressEvent, error) {
	var event processing.ProgressEvent
	var current, total *int
	var unit *string
	err := row.Scan(
		&event.Sequence,
		&event.ID,
		&event.TenantID,
		&event.JobID,
		&event.WorkflowID,
		&event.RunID,
		&event.Retry,
		&event.Stage,
		&event.Status,
		&current,
		&total,
		&unit,
		&event.Reason,
		&event.ReasonCode,
		&event.OccurredAt,
	)
	if err != nil {
		return processing.ProgressEvent{}, err
	}
	if current != nil && total != nil && unit != nil {
		event.Progress = &processing.ProgressMetadata{
			Current: *current,
			Total:   *total,
			Unit:    *unit,
		}
	}
	event.ReasonMessage = event.Reason
	event.OccurredAt = event.OccurredAt.UTC()
	return event, nil
}
