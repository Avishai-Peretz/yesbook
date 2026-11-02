// Package localworkflow stands in for Cloud Workflows during local
// development: it runs the pipeline in the background and blocks the job
// when retries are exhausted. The attempt count matches deploy/workflow.yaml
// (one run plus max_retries: 3). Backoff is exponential with the same
// multiplier, but the delays are shorter so local failures do not wait the
// production initial delay of 30 seconds.
package localworkflow

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

const (
	defaultAttempts     = 4
	defaultInitial      = 2 * time.Second
	defaultMaxBackoff   = 16 * time.Second
	defaultMultiplier   = 2
	defaultDeadline     = 30 * time.Minute
	defaultBlockTimeout = 30 * time.Second
)

// Processor is the part of processing.Pipeline the runner drives.
type Processor interface {
	Process(ctx context.Context, tenantID, jobID, worker string) (document.Status, error)
	Block(ctx context.Context, tenantID, jobID, reason string) error
}

type Runner struct {
	p            Processor
	log          *slog.Logger
	attempts     int
	initial      time.Duration
	maxBackoff   time.Duration
	multiplier   int
	deadline     time.Duration
	blockTimeout time.Duration

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func New(p Processor, log *slog.Logger) *Runner {
	ctx, stop := context.WithCancel(context.Background())
	return &Runner{
		p: p, log: log, attempts: defaultAttempts, initial: defaultInitial, maxBackoff: defaultMaxBackoff,
		multiplier: defaultMultiplier, deadline: defaultDeadline, blockTimeout: defaultBlockTimeout,
		ctx: ctx, stop: stop,
	}
}

// Start returns immediately; processing continues in the background.
func (r *Runner) Start(_ context.Context, tenantID, jobID string) error {
	r.wg.Go(func() { r.run(tenantID, jobID) }) //nolint:contextcheck // runs outlive the request that started them and stop on Close
	return nil
}

func (r *Runner) run(tenantID, jobID string) {
	ctx, cancel := context.WithTimeout(r.ctx, r.deadline)
	defer cancel()
	log := r.log.With("tenant_id", tenantID, "job_id", jobID)
	worker := "local-" + jobID
	for attempt := 1; attempt <= r.attempts; attempt++ {
		attemptCtx := processing.WithRunIdentity(ctx, processing.RunIdentity{
			WorkflowID: worker,
			Retry:      attempt - 1,
		})
		status, err := r.p.Process(attemptCtx, tenantID, jobID, worker)
		if err == nil {
			log.InfoContext(ctx, "workflow finished", "status", status)
			return
		}
		log.WarnContext(ctx, "processing attempt failed", "attempt", attempt, "err", err)
		if attempt == r.attempts || ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(r.delay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	blockCtx, blockCancel := context.WithTimeout(context.WithoutCancel(ctx), r.blockTimeout)
	defer blockCancel()
	if err := r.p.Block(blockCtx, tenantID, jobID, "processing failed after retries"); err != nil {
		log.ErrorContext(ctx, "could not block job", "err", err)
	}
}

func (r *Runner) delay(attempt int) time.Duration {
	delay := r.initial
	for i := 1; i < attempt; i++ {
		if delay > r.maxBackoff/time.Duration(r.multiplier) {
			return r.maxBackoff
		}
		delay *= time.Duration(r.multiplier)
	}
	if delay > r.maxBackoff {
		return r.maxBackoff
	}
	return delay
}

// Close cancels running jobs and waits for them to stop.
func (r *Runner) Close() {
	r.stop()
	r.wg.Wait()
}
