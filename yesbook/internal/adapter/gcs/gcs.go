// Package gcs maps the logical buckets to private Cloud Storage buckets and
// signs V4 URLs for direct client uploads and downloads.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	cloudstorage "cloud.google.com/go/storage"

	"github.com/avi-p/yesbook/internal/storage"
)

type Store struct {
	client  *cloudstorage.Client
	buckets map[storage.Bucket]string
	now     func() time.Time
}

// New maps each logical bucket to a Cloud Storage bucket name. Signing uses
// the IAM signBlob API with the runtime service account, which needs
// roles/iam.serviceAccountTokenCreator on itself.
func New(client *cloudstorage.Client, buckets map[storage.Bucket]string) (*Store, error) {
	for _, b := range storage.Buckets {
		if buckets[b] == "" {
			return nil, fmt.Errorf("gcs: no bucket configured for %s", b)
		}
	}
	return &Store{client: client, buckets: buckets, now: time.Now}, nil
}

func (s *Store) object(ref storage.Ref) (*cloudstorage.ObjectHandle, error) {
	name, ok := s.buckets[ref.Bucket]
	if !ok || ref.Name == "" {
		return nil, fmt.Errorf("%w: %s", storage.ErrInvalidObject, ref)
	}
	return s.client.Bucket(name).Object(ref.Name), nil
}

func (s *Store) Stat(ctx context.Context, ref storage.Ref) (storage.ObjectInfo, error) {
	obj, err := s.object(ref)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	attrs, err := obj.Attrs(ctx)
	if errors.Is(err, cloudstorage.ErrObjectNotExist) {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat %s: %w", ref, err)
	}
	return storage.ObjectInfo{Size: attrs.Size}, nil
}

func (s *Store) Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error) {
	obj, err := s.object(ref)
	if err != nil {
		return nil, err
	}
	r, err := obj.NewReader(ctx)
	if errors.Is(err, cloudstorage.ErrObjectNotExist) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ref, err)
	}
	defer r.Close()
	if r.Attrs.Size > maxBytes {
		return nil, storage.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ref, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, storage.ErrTooLarge
	}
	return data, nil
}

func (s *Store) Write(ctx context.Context, ref storage.Ref, contentType string, data []byte) error {
	obj, err := s.object(ref)
	if err != nil {
		return err
	}
	w := obj.NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return fmt.Errorf("write %s: %w", ref, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("write %s: %w", ref, err)
	}
	return nil
}

// SignedUploadURL only signs uploads into the staging bucket, and Cloud
// Storage rejects bodies larger than maxBytes.
func (s *Store) SignedUploadURL(_ context.Context, ref storage.Ref, contentType string, maxBytes int64, ttl time.Duration) (storage.SignedURL, error) {
	if ref.Bucket != storage.BucketStaging {
		return storage.SignedURL{}, fmt.Errorf("%w: uploads are only allowed into %s", storage.ErrInvalidObject, storage.BucketStaging)
	}
	lengthRange := "0," + strconv.FormatInt(maxBytes, 10)
	return s.sign(ref, &cloudstorage.SignedURLOptions{
		Scheme:      cloudstorage.SigningSchemeV4,
		Method:      http.MethodPut,
		ContentType: contentType,
		Headers:     []string{"x-goog-content-length-range:" + lengthRange},
	}, ttl, map[string]string{"Content-Type": contentType, "x-goog-content-length-range": lengthRange})
}

func (s *Store) SignedDownloadURL(_ context.Context, ref storage.Ref, ttl time.Duration) (storage.SignedURL, error) {
	return s.sign(ref, &cloudstorage.SignedURLOptions{Scheme: cloudstorage.SigningSchemeV4, Method: http.MethodGet}, ttl, nil)
}

func (s *Store) sign(ref storage.Ref, opts *cloudstorage.SignedURLOptions, ttl time.Duration, headers map[string]string) (storage.SignedURL, error) {
	name, ok := s.buckets[ref.Bucket]
	if !ok || ref.Name == "" {
		return storage.SignedURL{}, fmt.Errorf("%w: %s", storage.ErrInvalidObject, ref)
	}
	opts.Expires = s.now().Add(ttl)
	u, err := s.client.Bucket(name).SignedURL(ref.Name, opts)
	if err != nil {
		return storage.SignedURL{}, fmt.Errorf("sign %s: %w", ref, err)
	}
	return storage.SignedURL{URL: u, Method: opts.Method, Headers: headers, ExpiresAt: opts.Expires}, nil
}
