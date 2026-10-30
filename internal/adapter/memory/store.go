// Package memory provides in-process repositories with the same semantics
// as the PostgreSQL adapter, for tests and quick local experiments.
package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/processing"
)

type outboxRow struct {
	event     document.Event
	published bool
	claimed   bool
}

type Store struct {
	mu       sync.Mutex
	jobs     map[string]document.JobRecord
	versions map[string]document.Version
	cache    map[string]string
	quality  map[string][]document.QualityResult
	attempts []document.Attempt
	progress []processing.ProgressEvent
	outbox   []outboxRow
}

var _ processing.ProgressSink = (*Store)(nil)

func New() *Store {
	return &Store{
		jobs:     make(map[string]document.JobRecord),
		versions: make(map[string]document.Version),
		cache:    make(map[string]string),
		quality:  make(map[string][]document.QualityResult),
	}
}

func (s *Store) CreateJob(_ context.Context, j *document.Job, ev document.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := j.Record()
	if _, ok := s.jobs[r.ID]; ok {
		return document.ErrConflict
	}
	r.Revision = 1
	s.jobs[r.ID] = r
	s.outbox = append(s.outbox, outboxRow{event: ev})
	j.Persisted(1)
	return nil
}

func (s *Store) GetJob(_ context.Context, tenantID, jobID string) (*document.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.jobs[jobID]
	if !ok || r.TenantID != tenantID {
		return nil, document.ErrNotFound
	}
	return document.RestoreJob(r), nil
}

func (s *Store) UpdateJob(_ context.Context, j *document.Job, events ...document.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateJobLocked(j, events...)
}

func (s *Store) updateJobLocked(j *document.Job, events ...document.Event) error {
	r := j.Record()
	cur, ok := s.jobs[r.ID]
	if !ok || cur.TenantID != r.TenantID {
		return document.ErrNotFound
	}
	if cur.Revision != r.Revision {
		return document.ErrConflict
	}
	r.Revision++
	s.jobs[r.ID] = r
	for _, ev := range events {
		s.outbox = append(s.outbox, outboxRow{event: ev})
	}
	j.Persisted(r.Revision)
	return nil
}

func (s *Store) FindCachedVersion(_ context.Context, cacheKey string) (document.Version, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.cache[cacheKey]
	if !ok {
		return document.Version{}, false, nil
	}
	return s.versions[id], true, nil
}

func (s *Store) CommitVersion(_ context.Context, c processing.Commit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.updateJobLocked(c.Job, c.Event); err != nil {
		return err
	}
	s.versions[c.Version.ID] = c.Version
	s.quality[c.Version.ID] = slices.Clone(c.Quality)
	if c.CacheKey != "" {
		if _, ok := s.cache[c.CacheKey]; !ok {
			s.cache[c.CacheKey] = c.Version.ID
		}
	}
	return nil
}

func (s *Store) GetVersion(_ context.Context, tenantID, versionID string) (document.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[versionID]
	if !ok || v.TenantID != tenantID {
		return document.Version{}, document.ErrNotFound
	}
	return v, nil
}

func (s *Store) RecordAttempt(_ context.Context, a document.Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	return nil
}

func (s *Store) Spend(_ context.Context, jobID string) (document.Spend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sp document.Spend
	for _, a := range s.attempts {
		if a.JobID == jobID {
			sp.Add(a)
		}
	}
	return sp, nil
}

func (s *Store) RecordProgress(_ context.Context, event processing.ProgressEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[event.JobID]
	if !ok || job.TenantID != event.TenantID {
		return document.ErrNotFound
	}
	event.Sequence = int64(len(s.progress) + 1)
	event.OccurredAt = event.OccurredAt.UTC()
	s.progress = append(s.progress, cloneProgressEvent(event))
	return nil
}

func (s *Store) ProgressEvents(_ context.Context, tenantID, jobID string) ([]processing.ProgressEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok || job.TenantID != tenantID {
		return nil, document.ErrNotFound
	}
	events := make([]processing.ProgressEvent, 0)
	for _, event := range s.progress {
		if event.JobID == jobID && event.TenantID == tenantID {
			events = append(events, cloneProgressEvent(event))
		}
	}
	return events, nil
}

func cloneProgressEvent(event processing.ProgressEvent) processing.ProgressEvent {
	if event.Progress != nil {
		progress := *event.Progress
		event.Progress = &progress
	}
	return event
}

func (s *Store) ProcessPending(ctx context.Context, limit int, publish outbox.PublishFunc) (int, error) {
	events, idx := s.claimPending(limit)
	if len(events) == 0 {
		return 0, nil
	}
	finished := false
	defer func() {
		if !finished {
			s.finishClaim(idx, nil)
		}
	}()
	published, err := publish(ctx, events)
	s.finishClaim(idx, published)
	finished = true
	return len(published), err
}

func (s *Store) claimPending(limit int) ([]document.Event, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]document.Event, 0, limit)
	idx := make([]int, 0, limit)
	for i, row := range s.outbox {
		if row.published || row.claimed || len(events) >= limit {
			continue
		}
		s.outbox[i].claimed = true
		idx = append(idx, i)
		events = append(events, row.event)
	}
	return events, idx
}

func (s *Store) finishClaim(idx []int, published []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range idx {
		if slices.Contains(published, s.outbox[i].event.ID) {
			s.outbox[i].published = true
		}
		s.outbox[i].claimed = false
	}
}

func (s *Store) Backlog(context.Context) (outbox.Backlog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b outbox.Backlog
	for _, row := range s.outbox {
		if row.published {
			continue
		}
		b.Pending++
		if b.Oldest.IsZero() || row.event.OccurredAt.Before(b.Oldest) {
			b.Oldest = row.event.OccurredAt
		}
	}
	return b, nil
}

// Events returns every outbox event written so far.
func (s *Store) Events() []document.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]document.Event, 0, len(s.outbox))
	for _, row := range s.outbox {
		out = append(out, row.event)
	}
	return out
}

// Attempts returns every recorded extractor attempt.
func (s *Store) Attempts() []document.Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.attempts)
}

// Quality returns the quality results committed with a version.
func (s *Store) Quality(versionID string) []document.QualityResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.quality[versionID])
}

// SetJobUpdatedAt backdates a job, for lease tests.
func (s *Store) SetJobUpdatedAt(jobID string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.jobs[jobID]
	r.UpdatedAt = t
	s.jobs[jobID] = r
}
