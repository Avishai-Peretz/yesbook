// Package app is the composition root shared by the binaries: it reads
// configuration from the environment and wires adapters. Extractor wiring
// lives in app/extraction so the API binaries do not link the model SDKs.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	cloudstorage "cloud.google.com/go/storage"

	"github.com/avi-p/yesbook/internal/adapter/gcs"
	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/storage"
)

// Blobs is everything the binaries need from object storage.
type Blobs interface {
	Stat(ctx context.Context, ref storage.Ref) (storage.ObjectInfo, error)
	Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error)
	Write(ctx context.Context, ref storage.Ref, contentType string, data []byte) error
	Delete(ctx context.Context, ref storage.Ref) error
	DeletePrefix(ctx context.Context, bucket storage.Bucket, prefix string) error
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

// Runtime is the PostgreSQL pool and Cloud Storage client shared by the
// Cloud Run binaries. Close closes the pool and then the clients, newest first.
type Runtime struct {
	Pool    *pgxpool.Pool
	Blobs   *gcs.Store
	closers Closers
}

// OpenRuntime connects to PostgreSQL and opens the configured buckets.
func OpenRuntime(ctx context.Context, databaseURL string) (*Runtime, error) {
	var closers Closers
	pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	blobs, err := GCSBlobs(ctx, &closers)
	if err != nil {
		pool.Close()
		_ = closers.Close()
		return nil, err
	}
	return &Runtime{Pool: pool, Blobs: blobs, closers: closers}, nil
}

// Add registers a closer released before the storage client and after the pool.
func (r *Runtime) Add(cl io.Closer) { r.closers.Add(cl) }

// Close closes the database pool and then every registered client, newest first.
func (r *Runtime) Close() error {
	r.Pool.Close()
	return r.closers.Close()
}

// Group tracks background goroutines started by a process.
type Group struct {
	wg sync.WaitGroup
}

// Go runs fn until it returns. Wait blocks until every call has returned.
func (g *Group) Go(fn func()) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		fn()
	}()
}

// Wait blocks until every goroutine started by Go has returned.
func (g *Group) Wait() { g.wg.Wait() }
