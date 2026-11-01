// Package outbox relays events committed to the outbox table to the
// document events topic, right after each commit and on a periodic sweep.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

// PublishFunc publishes a batch in order and returns the IDs it published.
type PublishFunc func(ctx context.Context, events []document.Event) ([]string, error)

type Store interface {
	// ProcessPending locks up to limit unpublished events, passes them to
	// publish and marks the returned IDs as published.
	ProcessPending(ctx context.Context, limit int, publish PublishFunc) (int, error)
	Backlog(ctx context.Context) (Backlog, error)
}

type Publisher interface {
	Publish(ctx context.Context, ev document.Event) error
}

// Backlog describes unpublished events, for outbox-age alerts.
type Backlog struct {
	Pending int
	Oldest  time.Time
}

type Relay struct {
	store     Store
	publisher Publisher
	log       *slog.Logger
	batchSize int
	now       func() time.Time
}

func NewRelay(store Store, publisher Publisher, log *slog.Logger) *Relay {
	return &Relay{store: store, publisher: publisher, log: log, batchSize: 100, now: time.Now}
}

// Drain publishes pending events until none are left.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := r.store.ProcessPending(ctx, r.batchSize, r.publishBatch)
		total += n
		if err != nil {
			return total, fmt.Errorf("publish outbox events: %w", err)
		}
		if n < r.batchSize {
			return total, nil
		}
	}
}

// publishBatch stops at the first failure so events of a job stay in order.
func (r *Relay) publishBatch(ctx context.Context, events []document.Event) ([]string, error) {
	published := make([]string, 0, len(events))
	for _, ev := range events {
		if err := r.publisher.Publish(ctx, ev); err != nil {
			return published, fmt.Errorf("event %s: %w", ev.ID, err)
		}
		published = append(published, ev.ID)
	}
	return published, nil
}

// Sweep drains the outbox and logs the remaining backlog. Alert on the
// oldest_age_seconds attribute of the "outbox backlog" log entry.
func (r *Relay) Sweep(ctx context.Context) error {
	n, err := r.Drain(ctx)
	if n > 0 {
		r.log.InfoContext(ctx, "outbox events published", "count", n)
	}
	b, berr := r.store.Backlog(ctx)
	if berr != nil {
		return fmt.Errorf("read outbox backlog: %w", berr)
	}
	age := 0.0
	if !b.Oldest.IsZero() {
		age = r.now().Sub(b.Oldest).Seconds()
	}
	r.log.InfoContext(ctx, "outbox backlog", "pending", b.Pending, "oldest_age_seconds", age)
	return err
}

// Run sweeps whenever wake fires (a commit notification) and every interval,
// until ctx is cancelled. Failed sweeps are logged and retried on the next tick.
func (r *Relay) Run(ctx context.Context, wake <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "outbox sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				wake = nil
			}
		case <-ticker.C:
		}
	}
}
