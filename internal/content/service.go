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

// SignedAsset is a time-limited download for a figure asset.
type SignedAsset struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"`
}

// BlockView is a published block with optional signed asset URLs for figures.
type BlockView struct {
	Kind       document.BlockKind  `json:"kind"`
	Text       string              `json:"text,omitempty"`
	Rows       [][]string          `json:"rows,omitempty"`
	Page       int                 `json:"page"`
	EndPage    int                 `json:"end_page,omitempty"`
	Direction  document.Direction  `json:"direction,omitempty"`
	BBox       *document.BBox      `json:"bbox,omitempty"`
	AssetURL   string              `json:"asset_url,omitempty"`
	Asset      *SignedAsset        `json:"asset,omitempty"`
	Provenance document.Provenance `json:"provenance"`
}

// ContentView is published content with signed figure assets.
type ContentView struct {
	VersionID       string                `json:"version_id"`
	JobID           string                `json:"job_id"`
	Status          document.Status       `json:"status"`
	Coverage        float64               `json:"coverage"`
	PipelineVersion string                `json:"pipeline_version"`
	Pages           int                   `json:"pages"`
	Blocks          []BlockView           `json:"blocks"`
	Review          []document.ReviewItem `json:"review,omitempty"`
	Missing         []document.ReviewItem `json:"missing,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
}

type Result struct {
	Content  ContentView
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
	view, err := s.signContent(ctx, c)
	if err != nil {
		return Result{}, err
	}
	url, err := s.objects.SignedDownloadURL(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: v.OriginalObject}, s.urlTTL)
	if err != nil {
		return Result{}, fmt.Errorf("sign original URL: %w", err)
	}
	return Result{Content: view, Original: url}, nil
}

func (s *Service) signContent(ctx context.Context, c document.Content) (ContentView, error) {
	blocks := make([]BlockView, 0, len(c.Blocks))
	for _, b := range c.Blocks {
		view := BlockView{
			Kind:       b.Kind,
			Text:       b.Text,
			Rows:       b.Rows,
			Page:       b.Page,
			EndPage:    b.EndPage,
			Direction:  b.Direction,
			BBox:       b.BBox,
			Provenance: b.Provenance,
		}
		if b.Asset != nil && b.Asset.Object != "" {
			signed, err := s.objects.SignedDownloadURL(ctx, storage.Ref{
				Bucket: storage.BucketDerived,
				Name:   b.Asset.Object,
			}, s.urlTTL)
			if err != nil {
				return ContentView{}, fmt.Errorf("sign asset URL: %w", err)
			}
			view.AssetURL = signed.URL
			view.Asset = &SignedAsset{URL: signed.URL, ContentType: b.Asset.ContentType}
		}
		blocks = append(blocks, view)
	}
	return ContentView{
		VersionID:       c.VersionID,
		JobID:           c.JobID,
		Status:          c.Status,
		Coverage:        c.Coverage,
		PipelineVersion: c.PipelineVersion,
		Pages:           c.Pages,
		Blocks:          blocks,
		Review:          c.Review,
		Missing:         c.Missing,
		CreatedAt:       c.CreatedAt,
	}, nil
}
