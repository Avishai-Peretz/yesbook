package postgres

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/storage"
)

// DeleteDocument hard-deletes the tenant's job and GC's shared version/original
// when no other job still references them.
func (s *Store) DeleteDocument(ctx context.Context, tenantID, jobID string) (ingest.DeletePlan, error) {
	var plan ingest.DeletePlan
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			versionID string
			fileHash  string
			staging   = path.Join(tenantID, jobID+".pdf")
		)
		err := tx.QueryRow(ctx,
			`SELECT version_id, file_hash FROM jobs WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
			jobID, tenantID).Scan(&versionID, &fileHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return document.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock job: %w", err)
		}

		plan.TenantID = tenantID
		plan.DeleteRefs = append(plan.DeleteRefs, storage.Ref{Bucket: storage.BucketStaging, Name: staging})
		plan.DeletePrefix = append(plan.DeletePrefix, storage.Ref{
			Bucket: storage.BucketEvidence,
			Name:   path.Join(tenantID, jobID),
		})

		if _, err := tx.Exec(ctx, `DELETE FROM attempts WHERE job_id = $1`, jobID); err != nil {
			return fmt.Errorf("delete attempts: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE job_id = $1`, jobID); err != nil {
			return fmt.Errorf("delete outbox: %w", err)
		}
		// processing_events cascade with jobs.

		if versionID != "" {
			var otherRefs int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM jobs WHERE version_id = $1 AND id <> $2`, versionID, jobID).Scan(&otherRefs); err != nil {
				return fmt.Errorf("count version refs: %w", err)
			}
			if otherRefs == 0 {
				var contentObject, originalObject string
				err := tx.QueryRow(ctx,
					`SELECT content_object, original_object FROM versions WHERE id = $1 AND tenant_id = $2`,
					versionID, tenantID).Scan(&contentObject, &originalObject)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("load version: %w", err)
				}
				if err == nil {
					if _, err := tx.Exec(ctx, `DELETE FROM quality_results WHERE version_id = $1`, versionID); err != nil {
						return fmt.Errorf("delete quality: %w", err)
					}
					if _, err := tx.Exec(ctx, `DELETE FROM cache_keys WHERE version_id = $1`, versionID); err != nil {
						return fmt.Errorf("delete cache keys: %w", err)
					}
					if _, err := tx.Exec(ctx, `DELETE FROM versions WHERE id = $1`, versionID); err != nil {
						return fmt.Errorf("delete version: %w", err)
					}
					plan.DropVersion = true
					plan.VersionID = versionID
					plan.ContentObject = contentObject
					plan.OriginalObject = originalObject
				}
			} else {
				// versions.job_id REFERENCES jobs(id) without ON DELETE; move the
				// provenance pointer to a surviving job so this delete is not blocked.
				if _, err := tx.Exec(ctx, `
					UPDATE versions
					   SET job_id = (
					     SELECT id FROM jobs
					      WHERE version_id = $1 AND id <> $2
					      ORDER BY created_at ASC
					      LIMIT 1
					   )
					 WHERE id = $1 AND job_id = $2`, versionID, jobID); err != nil {
					return fmt.Errorf("reassign version job: %w", err)
				}
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM jobs WHERE id = $1 AND tenant_id = $2`, jobID, tenantID); err != nil {
			return fmt.Errorf("delete job: %w", err)
		}

		if plan.DropVersion && plan.OriginalObject != "" {
			var originalRefs int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM versions WHERE tenant_id = $1 AND original_object = $2`,
				tenantID, plan.OriginalObject).Scan(&originalRefs); err != nil {
				return fmt.Errorf("count original refs: %w", err)
			}
			var hashJobs int
			if fileHash != "" {
				if err := tx.QueryRow(ctx,
					`SELECT count(*) FROM jobs WHERE tenant_id = $1 AND file_hash = $2`,
					tenantID, fileHash).Scan(&hashJobs); err != nil {
					return fmt.Errorf("count hash jobs: %w", err)
				}
			}
			if originalRefs == 0 && hashJobs == 0 {
				plan.DropOriginal = true
			}
		}
		return nil
	})
	if err != nil {
		return ingest.DeletePlan{}, err
	}
	return plan, nil
}
