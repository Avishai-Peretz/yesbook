package store

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
)

// Memory is an in-process Store for local development and tests.
type Memory struct {
	mu     sync.Mutex
	jobs   map[string]job.Job
	events []outbox.Event
}

func NewMemory() *Memory {
	return &Memory{jobs: make(map[string]job.Job)}
}

func (m *Memory) CreateJob(_ context.Context, j job.Job, ev outbox.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; ok {
		return fmt.Errorf("job %s already exists", j.ID)
	}
	m.jobs[j.ID] = j
	m.events = append(m.events, ev)
	return nil
}

func (m *Memory) GetJob(_ context.Context, tenantID, jobID string) (job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok || j.TenantID != tenantID {
		return job.Job{}, ErrNotFound
	}
	return j, nil
}

func (m *Memory) TransitionJob(_ context.Context, tenantID, jobID string, from, to job.Status, now time.Time, ev outbox.Event) (job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok || j.TenantID != tenantID {
		return job.Job{}, ErrNotFound
	}
	if j.Status != from {
		return job.Job{}, ErrConflict
	}
	j.Status = to
	j.UpdatedAt = now
	m.jobs[jobID] = j
	m.events = append(m.events, ev)
	return j, nil
}

// Events returns a copy of all outbox events written so far.
func (m *Memory) Events() []outbox.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.events)
}
