package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

func (s *Store) RecordAttempt(ctx context.Context, a document.Attempt) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO attempts (id, job_id, target_id, extractor, extractor_version, succeeded, error, cost_micros, started_at, finished_at, reason_code, reason_message, decision)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		a.ID, a.JobID, a.TargetID, a.Extractor, a.ExtractorVersion, a.Succeeded, a.Error, a.CostMicros, a.StartedAt, a.FinishedAt,
		a.ReasonCode, a.ReasonMessage, a.Decision)
	if err != nil {
		return fmt.Errorf("insert attempt: %w", err)
	}
	return nil
}

func (s *Store) Spend(ctx context.Context, jobID string) (document.Spend, error) {
	var sp document.Spend
	var first *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(cost_micros), 0), min(started_at) FROM attempts WHERE job_id = $1`, jobID).
		Scan(&sp.Attempts, &sp.CostMicros, &first)
	if err != nil {
		return document.Spend{}, fmt.Errorf("sum attempts: %w", err)
	}
	if first != nil {
		sp.FirstAttemptAt = first.UTC()
	}
	return sp, nil
}
