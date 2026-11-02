package memory

import (
	"context"
	"path"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

// DeleteDocument hard-deletes the tenant's job and GC's shared version/original
// when no other job still references them.
func (s *Store) DeleteDocument(_ context.Context, tenantID, jobID string) (ingest.DeletePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok || job.TenantID != tenantID {
		return ingest.DeletePlan{}, document.ErrNotFound
	}

	plan := ingest.DeletePlan{TenantID: tenantID}
	plan.DeleteRefs = append(plan.DeleteRefs, storage.Ref{
		Bucket: storage.BucketStaging,
		Name:   path.Join(tenantID, jobID+".pdf"),
	})
	plan.DeletePrefix = append(plan.DeletePrefix, storage.Ref{
		Bucket: storage.BucketEvidence,
		Name:   path.Join(tenantID, jobID),
	})

	attempts := make([]document.Attempt, 0, len(s.attempts))
	for _, a := range s.attempts {
		if a.JobID != jobID {
			attempts = append(attempts, a)
		}
	}
	s.attempts = attempts

	events := make([]processing.ProgressEvent, 0, len(s.progress))
	for _, e := range s.progress {
		if e.JobID != jobID {
			events = append(events, e)
		}
	}
	s.progress = events

	outboxRows := make([]outboxRow, 0, len(s.outbox))
	for _, row := range s.outbox {
		if row.event.JobID != jobID {
			outboxRows = append(outboxRows, row)
		}
	}
	s.outbox = outboxRows

	versionID := job.VersionID
	if versionID != "" {
		otherRefs := 0
		var successor string
		for id, other := range s.jobs {
			if id != jobID && other.VersionID == versionID {
				otherRefs++
				if successor == "" || other.CreatedAt.Before(s.jobs[successor].CreatedAt) {
					successor = id
				}
			}
		}
		if otherRefs == 0 {
			if v, ok := s.versions[versionID]; ok {
				plan.DropVersion = true
				plan.VersionID = versionID
				plan.ContentObject = v.ContentObject
				plan.OriginalObject = v.OriginalObject
				delete(s.quality, versionID)
				for key, vid := range s.cache {
					if vid == versionID {
						delete(s.cache, key)
					}
				}
				delete(s.versions, versionID)
			}
		} else if v, ok := s.versions[versionID]; ok && v.JobID == jobID && successor != "" {
			v.JobID = successor
			s.versions[versionID] = v
		}
	}

	delete(s.jobs, jobID)

	if plan.DropVersion && plan.OriginalObject != "" {
		originalRefs := 0
		for _, v := range s.versions {
			if v.TenantID == tenantID && v.OriginalObject == plan.OriginalObject {
				originalRefs++
			}
		}
		hashJobs := 0
		if job.FileHash != "" {
			for _, other := range s.jobs {
				if other.TenantID == tenantID && other.FileHash == job.FileHash {
					hashJobs++
				}
			}
		}
		if originalRefs == 0 && hashJobs == 0 {
			plan.DropOriginal = true
		}
	}
	return plan, nil
}
