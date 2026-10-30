// Package postgres implements the repositories and the transactional outbox
// on Cloud SQL for PostgreSQL.
package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// Store implements the repositories and the transactional outbox.
type Store struct {
	pool     *pgxpool.Pool
	listenWG sync.WaitGroup
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Wait blocks until every listener started by Listen has returned.
// Cancel the listener context before calling Wait.
func (s *Store) Wait() {
	s.listenWG.Wait()
}
