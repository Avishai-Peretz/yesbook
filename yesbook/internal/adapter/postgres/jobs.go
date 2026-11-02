package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
)

const jobColumns = `id, tenant_id, file_name, size_bytes, status, file_hash, version_id, reason, extraction_mode, claimed_by, revision, created_at, updated_at`

func (s *Store) CreateJob(ctx context.Context, j *document.Job, ev document.Event) error {
	r := j.Record()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO jobs (`+jobColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, $11, $12)`,
			r.ID, r.TenantID, r.FileName, r.SizeBytes, r.Status, r.FileHash, r.VersionID, r.Reason, r.ExtractionMode, r.ClaimedBy, r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert job: %w", err)
		}
		return insertEvents(ctx, tx, ev)
	})
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	j.Persisted(1)
	return nil
}

func (s *Store) GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1 AND tenant_id = $2`, jobID, tenantID)
	var r document.JobRecord
	err := row.Scan(&r.ID, &r.TenantID, &r.FileName, &r.SizeBytes, &r.Status, &r.FileHash,
		&r.VersionID, &r.Reason, &r.ExtractionMode, &r.ClaimedBy, &r.Revision, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, document.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return document.RestoreJob(r), nil
}

func (s *Store) UpdateJob(ctx context.Context, j *document.Job, events ...document.Event) error {
	var revision int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		revision, err = updateJob(ctx, tx, j)
		if err != nil {
			return err
		}
		return insertEvents(ctx, tx, events...)
	})
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	j.Persisted(revision)
	return nil
}

// updateJob writes j if nobody else changed it since it was loaded.
func updateJob(ctx context.Context, tx pgx.Tx, j *document.Job) (int, error) {
	r := j.Record()
	var revision int
	err := tx.QueryRow(ctx,
		`UPDATE jobs SET status = $1, file_hash = $2, version_id = $3, reason = $4, extraction_mode = $5, claimed_by = $6,
		        updated_at = $7, revision = revision + 1
		 WHERE id = $8 AND tenant_id = $9 AND revision = $10
		 RETURNING revision`,
		r.Status, r.FileHash, r.VersionID, r.Reason, r.ExtractionMode, r.ClaimedBy, r.UpdatedAt, r.ID, r.TenantID, r.Revision).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1 AND tenant_id = $2)`, r.ID, r.TenantID).Scan(&exists); err != nil {
			return 0, fmt.Errorf("check job: %w", err)
		}
		if exists {
			return 0, document.ErrConflict
		}
		return 0, document.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("update job: %w", err)
	}
	return revision, nil
}

// StuckJobs counts jobs queued or processing without progress since before.
func (s *Store) StuckJobs(ctx context.Context, before time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE status IN ('queued', 'processing') AND updated_at < $1`, before).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count stuck jobs: %w", err)
	}
	return n, nil
}
