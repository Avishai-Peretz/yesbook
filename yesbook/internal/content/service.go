// Package content implements the Content API use cases: returning accepted
// content of a document to education services within the caller's tenant.
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

const maxContentBytes = 64 << 20

type JobReader interface {
	GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error)
}

type VersionReader interface {
	GetVersion(ctx context.Context, tenantID, versionID string) (document.Version, error)
}

type Objects interface {
	Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error)
	SignedDownloadURL(ctx context.Context, ref storage.Ref, ttl time.Duration) (storage.SignedURL, error)
}

var (
	ErrNotPublished = errors.New("document has no published content")
	ErrQualityGate  = errors.New("content needs review and was not requested")
)

// Query controls the quality gate. Ready and partial content is always
// returned; content that needs review only when IncludeReview is set.
type Query struct {
	IncludeReview bool
}

type Result struct {
	Content  document.Content
	Original storage.SignedURL
}

type Service struct {
	jobs     JobReader
	versions VersionReader
	objects  Objects
	urlTTL   time.Duration
}

func NewService(jobs JobReader, versions VersionReader, objects Objects, urlTTL time.Duration) *Service {
	return &Service{jobs: jobs, versions: versions, objects: objects, urlTTL: urlTTL}
}

// ForJob returns the content of the version a job published.
func (s *Service) ForJob(ctx context.Context, tenantID, jobID string, q Query) (Result, error) {
	j, err := s.jobs.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return Result{}, err
	}
	if !j.Status().Published() {
		return Result{}, fmt.Errorf("%w: status is %s", ErrNotPublished, j.Status())
	}
	return s.Version(ctx, tenantID, j.VersionID(), q)
}

func (s *Service) Version(ctx context.Context, tenantID, versionID string, q Query) (Result, error) {
	v, err := s.versions.GetVersion(ctx, tenantID, versionID)
	if err != nil {
		return Result{}, err
	}
	if v.Status == document.StatusNeedsReview && !q.IncludeReview {
		return Result{}, ErrQualityGate
	}
	raw, err := s.objects.Read(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: v.ContentObject}, maxContentBytes)
	if err != nil {
		return Result{}, fmt.Errorf("read content: %w", err)
	}
	var c document.Content
	if err := json.Unmarshal(raw, &c); err != nil {
		return Result{}, fmt.Errorf("decode content: %w", err)
	}
	url, err := s.objects.SignedDownloadURL(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: v.OriginalObject}, s.urlTTL)
	if err != nil {
		return Result{}, fmt.Errorf("sign original URL: %w", err)
	}
	return Result{Content: c, Original: url}, nil
}
