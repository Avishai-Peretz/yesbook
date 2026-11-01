// Package job defines a document processing job and its lifecycle.
package job

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

const (
	ContentTypePDF = "application/pdf"
	MaxSizeBytes   = 100 << 20
	maxFileNameLen = 255
)

// ErrInvalid is returned when a job request fails validation.
var ErrInvalid = errors.New("invalid job request")

// Job is one uploaded PDF and its processing state.
type Job struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	FileName      string    `json:"file_name"`
	ContentType   string    `json:"content_type"`
	SizeBytes     int64     `json:"size_bytes"`
	StagingObject string    `json:"-"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NewRequest holds the client-declared properties of a document.
type NewRequest struct {
	TenantID    string
	FileName    string
	ContentType string
	SizeBytes   int64
}

// New validates r and returns a job awaiting upload.
func New(id string, r NewRequest, now time.Time) (Job, error) {
	name := strings.TrimSpace(r.FileName)
	switch {
	case r.TenantID == "":
		return Job{}, fmt.Errorf("%w: tenant is required", ErrInvalid)
	case name == "" || len(name) > maxFileNameLen:
		return Job{}, fmt.Errorf("%w: file_name must be 1-%d characters", ErrInvalid, maxFileNameLen)
	case r.ContentType != ContentTypePDF:
		return Job{}, fmt.Errorf("%w: content_type must be %s", ErrInvalid, ContentTypePDF)
	case r.SizeBytes <= 0 || r.SizeBytes > MaxSizeBytes:
		return Job{}, fmt.Errorf("%w: size_bytes must be between 1 and %d", ErrInvalid, MaxSizeBytes)
	}
	return Job{
		ID:            id,
		TenantID:      r.TenantID,
		FileName:      name,
		ContentType:   r.ContentType,
		SizeBytes:     r.SizeBytes,
		StagingObject: StagingObject(r.TenantID, id),
		Status:        StatusAwaitingUpload,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// StagingObject returns the storage object name a job's upload is written to.
func StagingObject(tenantID, jobID string) string {
	return path.Join("staging", tenantID, jobID+".pdf")
}
