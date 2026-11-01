// Package localworkflow stands in for Cloud Workflows during local
// development: it runs the pipeline in the background with the same retry
// and block-on-failure policy as deploy/workflow.yaml.
package localworkflow

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

// Processor is the part of processing.Pipeline the runner drives.
type Processor interface {
	Process(ctx context.Context, tenantID, jobID, worker string) (document.Status, error)
	Block(ctx context.Context, tenantID, jobID, reason string) error
}

type Runner struct {
	p        Processor
	log      *slog.Logger
	attempts int
	backoff  time.Duration
	deadline time.Duration

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func New(p Processor, log *slog.Logger) *Runner {
	ctx, stop := context.WithCancel(context.Background())
	return &Runner{p: p, log: log, attempts: 3, backoff: 2 * time.Second, deadline: 30 * time.Minute, ctx: ctx, stop: stop}
}

// Start returns immediately; processing continues in the background.
func (r *Runner) Start(_ context.Context, tenantID, jobID string) error {
	r.wg.Go(func() { r.run(tenantID, jobID) })
	return nil
}

func (r *Runner) run(tenantID, jobID string) {
	ctx, cancel := context.WithTimeout(r.ctx, r.deadline)
	defer cancel()
	log := r.log.With("tenant_id", tenantID, "job_id", jobID)
	worker := "local-" + jobID
	for attempt := 1; attempt <= r.attempts; attempt++ {
		status, err := r.p.Process(ctx, tenantID, jobID, worker)
		if err == nil {
			log.InfoContext(ctx, "workflow finished", "status", status)
			return
		}
		log.WarnContext(ctx, "processing attempt failed", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			attempt = r.attempts
		case <-time.After(r.backoff * time.Duration(attempt)):
		}
	}
	if err := r.p.Block(context.WithoutCancel(ctx), tenantID, jobID, "processing failed after retries"); err != nil {
		log.ErrorContext(ctx, "could not block job", "err", err)
	}
}

// Close cancels running jobs and waits for them to stop.
func (r *Runner) Close() {
	r.stop()
	r.wg.Wait()
}
