package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/outbox"
)

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

// ProcessPending locks the oldest unpublished events, skipping rows another
// publisher holds, and marks the ones publish reports as sent. Marks for the
// accepted prefix are committed even when a later publish fails. A database
// failure after the broker accepted an event can deliver it again, so
// consumers must be idempotent.
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
		return 0, fmt.Errorf("process outbox: %w", err)
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
