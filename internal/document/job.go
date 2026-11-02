// Package document is the domain model of the Yesbook PDF service: jobs and
// their lifecycle, extracted content, quality results and published versions.
// It depends only on the standard library.
package document

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusAwaitingUpload Status = "awaiting_upload"
	StatusQueued         Status = "queued"
	StatusProcessing     Status = "processing"
	StatusReady          Status = "ready"
	StatusPartial        Status = "partial"
	StatusNeedsReview    Status = "needs_review"
	StatusBlocked        Status = "blocked"
)

// Final reports whether no further processing happens in status s.
func (s Status) Final() bool {
	switch s {
	case StatusReady, StatusPartial, StatusNeedsReview, StatusBlocked:
		return true
	default:
		return false
	}
}

// Published reports whether a job in status s has a version consumers can read.
func (s Status) Published() bool {
	return s == StatusReady || s == StatusPartial || s == StatusNeedsReview
}

const (
	ContentTypePDF = "application/pdf"
	MaxSizeBytes   = 100 << 20
	maxFileNameLen = 255
)

var (
	ErrInvalid    = errors.New("invalid request")
	ErrTransition = errors.New("invalid status transition")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("concurrent modification")
)

// Job is one uploaded PDF and its processing state. State changes go through
// its methods so every transition is validated in one place.
type Job struct {
	id             string
	tenantID       string
	fileName       string
	sizeBytes      int64
	status         Status
	fileHash       string
	versionID      string
	reason         string
	extractionMode ExtractionMode
	claimedBy      string
	revision       int
	createdAt      time.Time
	updatedAt      time.Time
}

// NewJobParams are the client-declared properties of a document.
type NewJobParams struct {
	TenantID       string
	FileName       string
	ContentType    string
	SizeBytes      int64
	ExtractionMode ExtractionMode
}

// NewJob validates p and returns a job awaiting upload.
func NewJob(id string, p NewJobParams, now time.Time) (*Job, error) {
	name := strings.TrimSpace(p.FileName)
	mode, err := ParseExtractionMode(string(p.ExtractionMode))
	if err != nil {
		return nil, err
	}
	switch {
	case id == "" || p.TenantID == "":
		return nil, fmt.Errorf("%w: id and tenant are required", ErrInvalid)
	case name == "" || len(name) > maxFileNameLen:
		return nil, fmt.Errorf("%w: file_name must be 1-%d characters", ErrInvalid, maxFileNameLen)
	case p.ContentType != ContentTypePDF:
		return nil, fmt.Errorf("%w: content_type must be %s", ErrInvalid, ContentTypePDF)
	case p.SizeBytes <= 0 || p.SizeBytes > MaxSizeBytes:
		return nil, fmt.Errorf("%w: size_bytes must be between 1 and %d", ErrInvalid, MaxSizeBytes)
	}
	return &Job{
		id:             id,
		tenantID:       p.TenantID,
		fileName:       name,
		sizeBytes:      p.SizeBytes,
		status:         StatusAwaitingUpload,
		extractionMode: mode,
		createdAt:      now,
		updatedAt:      now,
	}, nil
}

func (j *Job) ID() string                     { return j.id }
func (j *Job) TenantID() string               { return j.tenantID }
func (j *Job) FileName() string               { return j.fileName }
func (j *Job) SizeBytes() int64               { return j.sizeBytes }
func (j *Job) Status() Status                 { return j.status }
func (j *Job) FileHash() string               { return j.fileHash }
func (j *Job) VersionID() string              { return j.versionID }
func (j *Job) Reason() string                 { return j.reason }
func (j *Job) ExtractionMode() ExtractionMode { return j.extractionMode }
func (j *Job) ClaimedBy() string              { return j.claimedBy }
func (j *Job) CreatedAt() time.Time           { return j.createdAt }
func (j *Job) UpdatedAt() time.Time           { return j.updatedAt }

// SetExtractionMode updates the mode while the job has not started processing.
func (j *Job) SetExtractionMode(mode ExtractionMode, now time.Time) error {
	parsed, err := ParseExtractionMode(string(mode))
	if err != nil {
		return err
	}
	switch j.status {
	case StatusAwaitingUpload, StatusQueued:
	default:
		return fmt.Errorf("%w: extraction_mode can only change before processing", ErrTransition)
	}
	j.extractionMode = parsed
	j.updatedAt = now
	return nil
}

// StagingObject is the object name the client uploads the PDF to.
func (j *Job) StagingObject() string {
	return path.Join(j.tenantID, j.id+".pdf")
}

// OriginalObject is the object name of the verified original, shared by
// every job of the tenant that uploads the same file.
func (j *Job) OriginalObject() string {
	return path.Join(j.tenantID, j.fileHash+".pdf")
}

// Queue marks the upload as complete and the job ready for processing.
func (j *Job) Queue(now time.Time) error {
	if j.status != StatusAwaitingUpload {
		return j.transitionError(StatusQueued)
	}
	j.set(StatusQueued, now)
	return nil
}

// Claim gives worker exclusive processing rights. A job that is already
// processing can be reclaimed by the same worker (a retried task of the same
// execution), or by any worker once lease has passed without an update.
func (j *Job) Claim(worker string, lease time.Duration, now time.Time) error {
	if worker == "" {
		return fmt.Errorf("%w: worker is required", ErrInvalid)
	}
	switch {
	case j.status == StatusQueued:
	case j.status == StatusProcessing && (j.claimedBy == worker || now.Sub(j.updatedAt) > lease):
	default:
		return j.transitionError(StatusProcessing)
	}
	j.claimedBy = worker
	j.set(StatusProcessing, now)
	return nil
}

// RecordHash stores the SHA-256 of the verified file.
func (j *Job) RecordHash(hash string, now time.Time) error {
	if j.status != StatusProcessing {
		return fmt.Errorf("%w: hash can only be recorded while processing", ErrTransition)
	}
	if hash == "" {
		return fmt.Errorf("%w: hash is required", ErrInvalid)
	}
	j.fileHash = hash
	j.updatedAt = now
	return nil
}

// Complete records the outcome of processing. Published outcomes require a
// version and a blocked outcome requires a reason.
func (j *Job) Complete(outcome Status, versionID, reason string, now time.Time) error {
	if j.status != StatusProcessing || !outcome.Final() {
		return j.transitionError(outcome)
	}
	if outcome.Published() && versionID == "" {
		return fmt.Errorf("%w: version is required for %s", ErrInvalid, outcome)
	}
	if outcome == StatusBlocked && reason == "" {
		return fmt.Errorf("%w: reason is required for %s", ErrInvalid, outcome)
	}
	j.versionID = versionID
	j.reason = reason
	j.set(outcome, now)
	return nil
}

// Block stops a job that cannot be processed, from any non-final status.
func (j *Job) Block(reason string, now time.Time) error {
	if j.status.Final() {
		return j.transitionError(StatusBlocked)
	}
	if reason == "" {
		return fmt.Errorf("%w: reason is required", ErrInvalid)
	}
	j.reason = reason
	j.set(StatusBlocked, now)
	return nil
}

// Event returns the outbox event announcing the job's current status.
func (j *Job) Event(eventID string, now time.Time) Event {
	return Event{
		ID:         eventID,
		Type:       "document." + string(j.status),
		JobID:      j.id,
		TenantID:   j.tenantID,
		VersionID:  j.versionID,
		Status:     j.status,
		OccurredAt: now,
	}
}

func (j *Job) set(s Status, now time.Time) {
	j.status = s
	j.updatedAt = now
}

func (j *Job) transitionError(to Status) error {
	return fmt.Errorf("%w: %s -> %s", ErrTransition, j.status, to)
}

// JobRecord is the persisted form of a Job.
type JobRecord struct {
	ID             string
	TenantID       string
	FileName       string
	SizeBytes      int64
	Status         Status
	FileHash       string
	VersionID      string
	Reason         string
	ExtractionMode ExtractionMode
	ClaimedBy      string
	Revision       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (j *Job) Record() JobRecord {
	mode := j.extractionMode
	if mode == "" {
		mode = ModeAutomatic
	}
	return JobRecord{
		ID:             j.id,
		TenantID:       j.tenantID,
		FileName:       j.fileName,
		SizeBytes:      j.sizeBytes,
		Status:         j.status,
		FileHash:       j.fileHash,
		VersionID:      j.versionID,
		Reason:         j.reason,
		ExtractionMode: mode,
		ClaimedBy:      j.claimedBy,
		Revision:       j.revision,
		CreatedAt:      j.createdAt,
		UpdatedAt:      j.updatedAt,
	}
}

// RestoreJob rebuilds a job loaded by a repository.
func RestoreJob(r JobRecord) *Job {
	mode := r.ExtractionMode
	if mode == "" {
		mode = ModeAutomatic
	}
	return &Job{
		id:             r.ID,
		tenantID:       r.TenantID,
		fileName:       r.FileName,
		sizeBytes:      r.SizeBytes,
		status:         r.Status,
		fileHash:       r.FileHash,
		versionID:      r.VersionID,
		reason:         r.Reason,
		extractionMode: mode,
		claimedBy:      r.ClaimedBy,
		revision:       r.Revision,
		createdAt:      r.CreatedAt,
		updatedAt:      r.UpdatedAt,
	}
}

// Persisted is called by repositories after storing the job at revision, the
// optimistic-concurrency token checked by the next update.
func (j *Job) Persisted(revision int) {
	j.revision = revision
}
