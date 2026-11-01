// Package workflow starts document processing (Cloud Workflows in production).
package workflow

import (
	"context"
	"log/slog"
)

type Starter interface {
	// Start may be called more than once for the same job, so implementations
	// must be idempotent per job ID.
	Start(ctx context.Context, tenantID, jobID string) error
}

// LogStarter records start requests without running anything.
type LogStarter struct {
	Log *slog.Logger
}

func (s LogStarter) Start(ctx context.Context, tenantID, jobID string) error {
	s.Log.InfoContext(ctx, "workflow start requested", "tenant_id", tenantID, "job_id", jobID)
	return nil
}
