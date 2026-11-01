// Package postgres implements the repositories and the transactional outbox
// on Cloud SQL for PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/processing"
)

// NotifyChannel is signalled inside every transaction that writes an outbox
// event, so the publisher can relay it right after the commit.
const NotifyChannel = "yesbook_outbox"

// Connect opens a connection pool sized for one Cloud Run instance.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const jobColumns = `id, tenant_id, file_name, size_bytes, status, file_hash, version_id, reason, claimed_by, revision, created_at, updated_at`

func (s *Store) CreateJob(ctx context.Context, j *document.Job, ev document.Event) error {
	r := j.Record()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO jobs (`+jobColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $11)`,
			r.ID, r.TenantID, r.FileName, r.SizeBytes, r.Status, r.FileHash, r.VersionID, r.Reason, r.ClaimedBy, r.CreatedAt, r.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert job: %w", err)
		}
		return insertEvents(ctx, tx, ev)
	})
	if err != nil {
		return err
	}
	j.Persisted(1)
	return nil
}

func (s *Store) GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1 AND tenant_id = $2`, jobID, tenantID)
	var r document.JobRecord
	err := row.Scan(&r.ID, &r.TenantID, &r.FileName, &r.SizeBytes, &r.Status, &r.FileHash,
		&r.VersionID, &r.Reason, &r.ClaimedBy, &r.Revision, &r.CreatedAt, &r.UpdatedAt)
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
		return err
	}
	j.Persisted(revision)
	return nil
}

// updateJob writes j if nobody else changed it since it was loaded.
func updateJob(ctx context.Context, tx pgx.Tx, j *document.Job) (int, error) {
	r := j.Record()
	var revision int
	err := tx.QueryRow(ctx,
		`UPDATE jobs SET status = $1, file_hash = $2, version_id = $3, reason = $4, claimed_by = $5,
		        updated_at = $6, revision = revision + 1
		 WHERE id = $7 AND tenant_id = $8 AND revision = $9
		 RETURNING revision`,
		r.Status, r.FileHash, r.VersionID, r.Reason, r.ClaimedBy, r.UpdatedAt, r.ID, r.TenantID, r.Revision).Scan(&revision)
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

func insertEvents(ctx context.Context, tx pgx.Tx, events ...document.Event) error {
	for _, ev := range events {
		payload, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO outbox (id, event_type, job_id, tenant_id, payload, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			ev.ID, ev.Type, ev.JobID, ev.TenantID, payload, ev.OccurredAt)
		if err != nil {
			return fmt.Errorf("insert outbox event: %w", err)
		}
	}
	if len(events) > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, NotifyChannel); err != nil {
			return fmt.Errorf("notify outbox: %w", err)
		}
	}
	return nil
}

const versionColumns = `id, tenant_id, job_id, file_hash, pipeline_version, status, coverage, content_object, original_object, created_at`

func scanVersion(row pgx.Row) (document.Version, error) {
	var v document.Version
	err := row.Scan(&v.ID, &v.TenantID, &v.JobID, &v.FileHash, &v.PipelineVersion, &v.Status,
		&v.Coverage, &v.ContentObject, &v.OriginalObject, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return document.Version{}, document.ErrNotFound
	}
	if err != nil {
		return document.Version{}, fmt.Errorf("scan version: %w", err)
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, nil
}

func (s *Store) FindCachedVersion(ctx context.Context, cacheKey string) (document.Version, bool, error) {
	v, err := scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionColumns+` FROM versions WHERE id = (SELECT version_id FROM cache_keys WHERE key = $1)`, cacheKey))
	if errors.Is(err, document.ErrNotFound) {
		return document.Version{}, false, nil
	}
	if err != nil {
		return document.Version{}, false, err
	}
	return v, true, nil
}

func (s *Store) GetVersion(ctx context.Context, tenantID, versionID string) (document.Version, error) {
	return scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionColumns+` FROM versions WHERE id = $1 AND tenant_id = $2`, versionID, tenantID))
}

// CommitVersion stores the version, its quality results, its cache key and
// the job's final status in one transaction.
func (s *Store) CommitVersion(ctx context.Context, c processing.Commit) error {
	var revision int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		v := c.Version
		_, err := tx.Exec(ctx,
			`INSERT INTO versions (`+versionColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			v.ID, v.TenantID, v.JobID, v.FileHash, v.PipelineVersion, v.Status, v.Coverage, v.ContentObject, v.OriginalObject, v.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert version: %w", err)
		}
		for _, q := range c.Quality {
			checks, err := json.Marshal(q.Checks)
			if err != nil {
				return fmt.Errorf("encode quality checks: %w", err)
			}
			_, err = tx.Exec(ctx,
				`INSERT INTO quality_results (version_id, target_id, extractor, extractor_version, verdict, reason, checks)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				v.ID, q.TargetID, q.Extractor, q.ExtractorVersion, q.Verdict, q.Reason, checks)
			if err != nil {
				return fmt.Errorf("insert quality result: %w", err)
			}
		}
		if c.CacheKey != "" {
			_, err := tx.Exec(ctx,
				`INSERT INTO cache_keys (key, version_id, created_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING`,
				c.CacheKey, v.ID, v.CreatedAt)
			if err != nil {
				return fmt.Errorf("insert cache key: %w", err)
			}
		}
		if revision, err = updateJob(ctx, tx, c.Job); err != nil {
			return err
		}
		return insertEvents(ctx, tx, c.Event)
	})
	if err != nil {
		return err
	}
	c.Job.Persisted(revision)
	return nil
}

func (s *Store) RecordAttempt(ctx context.Context, a document.Attempt) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO attempts (id, job_id, target_id, extractor, extractor_version, succeeded, error, cost_micros, started_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.ID, a.JobID, a.TargetID, a.Extractor, a.ExtractorVersion, a.Succeeded, a.Error, a.CostMicros, a.StartedAt, a.FinishedAt)
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

// ProcessPending locks the oldest unpublished events, skipping rows another
// publisher holds, and marks the ones publish reports as sent. Marks are
// committed even when publish fails part-way, so nothing is sent twice.
func (s *Store) ProcessPending(ctx context.Context, limit int, publish outbox.PublishFunc) (int, error) {
	var published []string
	var publishErr error
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT payload FROM outbox WHERE published_at IS NULL ORDER BY seq LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return fmt.Errorf("select outbox: %w", err)
		}
		events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (document.Event, error) {
			var ev document.Event
			var payload []byte
			if err := row.Scan(&payload); err != nil {
				return ev, err
			}
			return ev, json.Unmarshal(payload, &ev)
		})
		if err != nil {
			return fmt.Errorf("read outbox: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		published, publishErr = publish(ctx, events)
		if len(published) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, published); err != nil {
			return fmt.Errorf("mark outbox published: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(published), publishErr
}

func (s *Store) Backlog(ctx context.Context) (outbox.Backlog, error) {
	var b outbox.Backlog
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `SELECT count(*), min(created_at) FROM outbox WHERE published_at IS NULL`).
		Scan(&b.Pending, &oldest)
	if err != nil {
		return outbox.Backlog{}, fmt.Errorf("read outbox backlog: %w", err)
	}
	if oldest != nil {
		b.Oldest = *oldest
	}
	return b, nil
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
