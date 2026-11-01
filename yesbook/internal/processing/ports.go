// Package processing implements the per-document extraction pipeline:
// verify, prepare, route, extract, validate and assemble, with bounded
// retries and an evidence store that lets a retried run resume.
package processing

import (
	"context"
	"errors"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

type JobRepository interface {
	GetJob(ctx context.Context, tenantID, jobID string) (*document.Job, error)
	UpdateJob(ctx context.Context, j *document.Job, events ...document.Event) error
}

type VersionRepository interface {
	FindCachedVersion(ctx context.Context, cacheKey string) (document.Version, bool, error)
	// CommitVersion stores the version, its quality results, its cache key
	// and the completed job with its event in one transaction.
	CommitVersion(ctx context.Context, c Commit) error
}

type AttemptLog interface {
	RecordAttempt(ctx context.Context, a document.Attempt) error
	Spend(ctx context.Context, jobID string) (document.Spend, error)
}

type ObjectStore interface {
	Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error)
	Write(ctx context.Context, ref storage.Ref, contentType string, data []byte) error
}

// Inspector reads the structure of a PDF.
type Inspector interface {
	Inspect(ctx context.Context, pdf []byte) (PDFInfo, error)
}

// Extractor turns one target into blocks. Name and Version identify the
// exact extractor build so results can be cached and reproduced.
type Extractor interface {
	Name() string
	Version() string
	Extract(ctx context.Context, req ExtractRequest) (document.Extraction, error)
}

var (
	ErrUnreadablePDF = errors.New("unreadable PDF")
	ErrEncryptedPDF  = errors.New("encrypted PDF")
	ErrClaimed       = errors.New("job is claimed by another worker")
)

type PDFInfo struct {
	Pages []PageInfo
}

// PageInfo describes one page: its 1-based number, embedded text layer and
// number of image objects.
type PageInfo struct {
	Number int
	Text   string
	Images int
}

type ExtractRequest struct {
	PDF     []byte
	Target  document.Target
	Attempt int
}

type Commit struct {
	Job      *document.Job
	Event    document.Event
	Version  document.Version
	CacheKey string
	Quality  []document.QualityResult
}
