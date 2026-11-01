// Package blob abstracts object storage (Cloud Storage in production).
package blob

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("object not found")

type ObjectInfo struct {
	Size int64
}

type UploadURL struct {
	URL       string
	ExpiresAt time.Time
}

type Store interface {
	// SignedUploadURL returns a URL that lets a client PUT exactly one object
	// with the given content type and at most maxBytes, until it expires.
	SignedUploadURL(ctx context.Context, object, contentType string, maxBytes int64, ttl time.Duration) (UploadURL, error)
	Stat(ctx context.Context, object string) (ObjectInfo, error)
}
