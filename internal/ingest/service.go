// Package ingest implements the Document API use cases: registering a job
// with a staging-only upload URL, submitting it for processing, and reading
// its status.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

type JobRepository interface {
	CreateJob(ctx context.Context, j *document.Job, ev document.Event) error
	GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error)
	UpdateJob(ctx context.Context, j *document.Job, events ...document.Event) error
}

type Uploads interface {
	SignedUploadURL(ctx context.Context, ref storage.Ref, contentType string, maxBytes int64, ttl time.Duration) (storage.SignedURL, error)
	Stat(ctx context.Context, ref storage.Ref) (storage.ObjectInfo, error)
}

type WorkflowStarter interface {
	// Start may be called more than once for the same job, so the workflow
	// must be idempotent per job ID.
	Start(ctx context.Context, tenantID, jobID string) error
}

var (
	ErrNotUploaded    = errors.New("file has not been uploaded")
	ErrSizeMismatch   = errors.New("uploaded size does not match declared size")
	ErrNotSubmittable = errors.New("document cannot be submitted in its current status")
	ErrStartFailed    = errors.New("could not start processing")
)

type Service struct {
	jobs       JobRepository
	uploads    Uploads
	workflows  WorkflowStarter
	uploadTTL  time.Duration
	now        func() time.Time
	newID      func() string
	configured map[string]bool
}

type Option func(*Service)

func WithUploadTTL(d time.Duration) Option   { return func(s *Service) { s.uploadTTL = d } }
func WithClock(now func() time.Time) Option  { return func(s *Service) { s.now = now } }
func WithIDGenerator(f func() string) Option { return func(s *Service) { s.newID = f } }

// WithConfiguredModes limits pinned extraction_mode values to the given tool names.
func WithConfiguredModes(configured map[string]bool) Option {
	return func(s *Service) { s.configured = configured }
}

func NewService(jobs JobRepository, uploads Uploads, workflows WorkflowStarter, opts ...Option) *Service {
	s := &Service{
		jobs:      jobs,
		uploads:   uploads,
		workflows: workflows,
		uploadTTL: 15 * time.Minute,
		now:       time.Now,
		newID:     document.NewID,
		configured: map[string]bool{
			string(document.ModePDFium): true,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type RegisterRequest struct {
	FileName       string
	ContentType    string
	SizeBytes      int64
	ExtractionMode document.ExtractionMode
}

type Registration struct {
	Job    *document.Job
	Upload storage.SignedURL
}

func (s *Service) validateMode(mode document.ExtractionMode) error {
	parsed, err := document.ParseExtractionMode(string(mode))
	if err != nil {
		return err
	}
	if !parsed.Pinned() {
		return nil
	}
	if s.configured == nil || !s.configured[parsed.ExtractorName()] {
		return fmt.Errorf("%w: extraction_mode %q is not configured", document.ErrInvalid, parsed)
	}
	return nil
}

// Register creates a job and returns a URL the client uploads the PDF to.
// The URL is signed before the job is stored so a signing failure leaves no
// orphaned job.
func (s *Service) Register(ctx context.Context, tenantID string, req RegisterRequest) (Registration, error) {
	if err := s.validateMode(req.ExtractionMode); err != nil {
		return Registration{}, err
	}
	now := s.now().UTC()
	j, err := document.NewJob(s.newID(), document.NewJobParams{
		TenantID:       tenantID,
		FileName:       req.FileName,
		ContentType:    req.ContentType,
		SizeBytes:      req.SizeBytes,
		ExtractionMode: req.ExtractionMode,
	}, now)
	if err != nil {
		return Registration{}, err
	}
	upload, err := s.uploads.SignedUploadURL(ctx, stagingRef(j), document.ContentTypePDF, j.SizeBytes(), s.uploadTTL)
	if err != nil {
		return Registration{}, fmt.Errorf("sign upload URL: %w", err)
	}
	if err := s.jobs.CreateJob(ctx, j, j.Event(s.newID(), now)); err != nil {
		return Registration{}, fmt.Errorf("create job: %w", err)
	}
	return Registration{Job: j, Upload: upload}, nil
}

func (s *Service) Get(ctx context.Context, tenantID, jobID string) (*document.Job, error) {
	return s.jobs.GetJob(ctx, tenantID, jobID)
}

type SubmitRequest struct {
	ExtractionMode document.ExtractionMode
}

// Submit confirms the upload and starts processing. Submitting a job that is
// already queued starts the workflow again, so clients can retry a failed start.
// Optional ExtractionMode updates the job before processing begins.
func (s *Service) Submit(ctx context.Context, tenantID, jobID string, req SubmitRequest) (*document.Job, error) {
	j, err := s.jobs.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	if req.ExtractionMode != "" {
		if err := s.validateMode(req.ExtractionMode); err != nil {
			return nil, err
		}
		now := s.now().UTC()
		if err := j.SetExtractionMode(req.ExtractionMode, now); err != nil {
			return nil, err
		}
		if err := s.jobs.UpdateJob(ctx, j); err != nil {
			return nil, fmt.Errorf("update extraction_mode: %w", err)
		}
	}
	if j.Status() == document.StatusAwaitingUpload {
		if j, err = s.queue(ctx, j); err != nil {
			return nil, err
		}
	}
	if j.Status() != document.StatusQueued {
		return nil, fmt.Errorf("%w: %s", ErrNotSubmittable, j.Status())
	}
	if err := s.workflows.Start(ctx, tenantID, jobID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStartFailed, err)
	}
	return j, nil
}

func (s *Service) queue(ctx context.Context, j *document.Job) (*document.Job, error) {
	info, err := s.uploads.Stat(ctx, stagingRef(j))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrNotUploaded
	}
	if err != nil {
		return nil, fmt.Errorf("stat upload: %w", err)
	}
	if info.Size != j.SizeBytes() {
		return nil, fmt.Errorf("%w: got %d bytes, declared %d", ErrSizeMismatch, info.Size, j.SizeBytes())
	}

	now := s.now().UTC()
	if err := j.Queue(now); err != nil {
		return nil, err
	}
	err = s.jobs.UpdateJob(ctx, j, j.Event(s.newID(), now))
	if errors.Is(err, document.ErrConflict) {
		return s.jobs.GetJob(ctx, j.TenantID(), j.ID())
	}
	if err != nil {
		return nil, fmt.Errorf("queue job: %w", err)
	}
	return j, nil
}

func stagingRef(j *document.Job) storage.Ref {
	return storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}
}
