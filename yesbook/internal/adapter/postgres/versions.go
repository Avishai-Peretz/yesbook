package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

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
		return fmt.Errorf("commit version: %w", err)
	}
	c.Job.Persisted(revision)
	return nil
}
