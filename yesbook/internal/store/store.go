// Package store persists jobs together with their outbox events.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
)

var (
	ErrNotFound = errors.New("job not found")
	ErrConflict = errors.New("job is not in the expected status")
)

// Store writes every job change and its outbox event atomically.
type Store interface {
	CreateJob(ctx context.Context, j job.Job, ev outbox.Event) error
	// GetJob returns ErrNotFound for jobs that belong to another tenant.
	GetJob(ctx context.Context, tenantID, jobID string) (job.Job, error)
	// TransitionJob moves a job from one status to another, returning
	// ErrConflict if the job is no longer in status from.
	TransitionJob(ctx context.Context, tenantID, jobID string, from, to job.Status, now time.Time, ev outbox.Event) (job.Job, error)
}
