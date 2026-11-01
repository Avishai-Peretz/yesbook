// Package logpublisher writes document events to the log instead of
// Pub/Sub, for local development.
package logpublisher

import (
	"context"
	"log/slog"

	"github.com/avi-p/yesbook/internal/document"
)

type Publisher struct {
	log *slog.Logger
}

func New(log *slog.Logger) *Publisher { return &Publisher{log: log} }

func (p *Publisher) Publish(ctx context.Context, ev document.Event) error {
	p.log.InfoContext(ctx, "document event",
		"event_id", ev.ID, "event_type", ev.Type, "tenant_id", ev.TenantID,
		"job_id", ev.JobID, "version_id", ev.VersionID, "status", ev.Status)
	return nil
}
