package postgres

import (
	"context"
	"log/slog"
	"time"
)

// Listen signals on the returned channel whenever an outbox event is
// committed. It reconnects after connection failures and closes the channel
// when ctx is cancelled. Signals are coalesced: a pending wake-up already
// covers every event committed before it is handled.
func (s *Store) Listen(ctx context.Context, log *slog.Logger) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go func() {
		defer close(wake)
		backoff := time.Second
		for {
			err := s.listenOnce(ctx, wake)
			if ctx.Err() != nil {
				return
			}
			log.WarnContext(ctx, "outbox listener disconnected", "err", err, "retry_in", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, time.Minute)
		}
	}()
	return wake
}

func (s *Store) listenOnce(ctx context.Context, wake chan<- struct{}) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+NotifyChannel); err != nil {
		return err
	}
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
