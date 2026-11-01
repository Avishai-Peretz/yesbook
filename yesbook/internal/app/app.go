// Package app is the composition root shared by the binaries: it reads
// configuration from the environment and wires adapters. Extractor wiring
// lives in app/extraction so the API binaries do not link the model SDKs.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	cloudstorage "cloud.google.com/go/storage"

	"github.com/avi-p/yesbook/internal/adapter/gcs"
	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/storage"
)

// Blobs is everything the binaries need from object storage.
type Blobs interface {
	Stat(ctx context.Context, ref storage.Ref) (storage.ObjectInfo, error)
	Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error)
	Write(ctx context.Context, ref storage.Ref, contentType string, data []byte) error
	SignedUploadURL(ctx context.Context, ref storage.Ref, contentType string, maxBytes int64, ttl time.Duration) (storage.SignedURL, error)
	SignedDownloadURL(ctx context.Context, ref storage.Ref, ttl time.Duration) (storage.SignedURL, error)
}

var (
	_ Blobs = (*gcs.Store)(nil)
	_ Blobs = (*localblob.Store)(nil)
)

// Closers releases clients in reverse order of creation.
type Closers []io.Closer

func (c *Closers) Add(cl io.Closer) { *c = append(*c, cl) }

func (c Closers) Close() error {
	var errs []error
	for i := len(c) - 1; i >= 0; i-- {
		errs = append(errs, c[i].Close())
	}
	return errors.Join(errs...)
}

// GCSBlobs maps the logical buckets from YESBOOK_BUCKET_* variables.
func GCSBlobs(ctx context.Context, closers *Closers) (*gcs.Store, error) {
	var req platform.RequireEnv
	buckets := map[storage.Bucket]string{
		storage.BucketStaging:   req.Get("YESBOOK_BUCKET_STAGING"),
		storage.BucketOriginals: req.Get("YESBOOK_BUCKET_ORIGINALS"),
		storage.BucketEvidence:  req.Get("YESBOOK_BUCKET_EVIDENCE"),
		storage.BucketDerived:   req.Get("YESBOOK_BUCKET_DERIVED"),
	}
	if err := req.Err(); err != nil {
		return nil, err
	}
	client, err := cloudstorage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	closers.Add(client)
	return gcs.New(client, buckets)
}

// LocalBlobs stores objects under dir and signs URLs served at baseURL.
func LocalBlobs(dir, baseURL, signingKey string) (*localblob.Store, error) {
	return localblob.New(dir, baseURL, []byte(signingKey))
}
