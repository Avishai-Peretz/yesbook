package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
)

//go:embed schema.sql
var schema string

// Migrate creates the tables if they do not exist.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// Postgres is a Store backed by Cloud SQL for PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

const jobColumns = `id, tenant_id, file_name, content_type, size_bytes, staging_object, status, created_at, updated_at`

func (p *Postgres) CreateJob(ctx context.Context, j job.Job, ev outbox.Event) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO jobs (`+jobColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			j.ID, j.TenantID, j.FileName, j.ContentType, j.SizeBytes, j.StagingObject, string(j.Status), j.CreatedAt, j.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert job: %w", err)
		}
		return insertEvent(ctx, tx, ev)
	})
}

func (p *Postgres) GetJob(ctx context.Context, tenantID, jobID string) (job.Job, error) {
	row := p.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = $1 AND tenant_id = $2`, jobID, tenantID)
	return scanJob(row)
}

func (p *Postgres) TransitionJob(ctx context.Context, tenantID, jobID string, from, to job.Status, now time.Time, ev outbox.Event) (job.Job, error) {
	var out job.Job
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE jobs SET status = $1, updated_at = $2
			 WHERE id = $3 AND tenant_id = $4 AND status = $5
			 RETURNING `+jobColumns,
			string(to), now, jobID, tenantID, string(from))
		j, err := scanJob(row)
		if errors.Is(err, ErrNotFound) {
			var exists bool
			err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1 AND tenant_id = $2)`, jobID, tenantID).Scan(&exists)
			if err != nil {
				return fmt.Errorf("check job: %w", err)
			}
			if exists {
				return ErrConflict
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = j
		return insertEvent(ctx, tx, ev)
	})
	return out, err
}

func insertEvent(ctx context.Context, tx pgx.Tx, ev outbox.Event) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("encode event payload: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, event_type, aggregate_id, tenant_id, payload, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		ev.ID, ev.Type, ev.AggregateID, ev.TenantID, string(payload), ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func scanJob(row pgx.Row) (job.Job, error) {
	var j job.Job
	var status string
	err := row.Scan(&j.ID, &j.TenantID, &j.FileName, &j.ContentType, &j.SizeBytes,
		&j.StagingObject, &status, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return job.Job{}, ErrNotFound
	}
	if err != nil {
		return job.Job{}, fmt.Errorf("scan job: %w", err)
	}
	j.Status = job.Status(status)
	return j, nil
}
