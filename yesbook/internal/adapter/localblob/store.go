// Package localblob stores objects on the local disk and emulates Cloud
// Storage signed URLs with HMAC-signed query parameters, for local runs.
package localblob

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/avi-p/yesbook/internal/storage"
)

// Store keeps each bucket in a subdirectory of root. Mount Handler on the
// server whose address is baseURL so signed URLs resolve.
type Store struct {
	root    string
	baseURL string
	key     []byte
	now     func() time.Time
}

func New(root, baseURL string, signingKey []byte) (*Store, error) {
	if len(signingKey) < 32 {
		return nil, errors.New("signing key must be at least 32 bytes")
	}
	for _, b := range storage.Buckets {
		if err := os.MkdirAll(filepath.Join(root, string(b)), 0o750); err != nil {
			return nil, fmt.Errorf("create bucket directory: %w", err)
		}
	}
	return &Store{root: root, baseURL: baseURL, key: signingKey, now: time.Now}, nil
}

func (s *Store) Stat(_ context.Context, ref storage.Ref) (storage.ObjectInfo, error) {
	p, err := s.path(ref)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat %s: %w", ref, err)
	}
	return storage.ObjectInfo{Size: fi.Size()}, nil
}

func (s *Store) Read(_ context.Context, ref storage.Ref, maxBytes int64) ([]byte, error) {
	p, err := s.path(ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ref, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ref, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, storage.ErrTooLarge
	}
	return data, nil
}

func (s *Store) Write(_ context.Context, ref storage.Ref, _ string, data []byte) error {
	p, err := s.path(ref)
	if err != nil {
		return err
	}
	return writeAtomic(p, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// SignedUploadURL only signs uploads into the staging bucket.
func (s *Store) SignedUploadURL(_ context.Context, ref storage.Ref, contentType string, maxBytes int64, ttl time.Duration) (storage.SignedURL, error) {
	if ref.Bucket != storage.BucketStaging {
		return storage.SignedURL{}, fmt.Errorf("%w: uploads are only allowed into %s", storage.ErrInvalidObject, storage.BucketStaging)
	}
	return s.signedURL(http.MethodPut, "/uploads/", ref, contentType, maxBytes, ttl)
}

func (s *Store) SignedDownloadURL(_ context.Context, ref storage.Ref, ttl time.Duration) (storage.SignedURL, error) {
	return s.signedURL(http.MethodGet, "/files/", ref, "", 0, ttl)
}

func (s *Store) signedURL(method, prefix string, ref storage.Ref, contentType string, maxBytes int64, ttl time.Duration) (storage.SignedURL, error) {
	if _, err := s.path(ref); err != nil {
		return storage.SignedURL{}, err
	}
	expires := s.now().Add(ttl).Truncate(time.Second)
	q := url.Values{}
	q.Set("exp", strconv.FormatInt(expires.Unix(), 10))
	if method == http.MethodPut {
		q.Set("ct", contentType)
		q.Set("max", strconv.FormatInt(maxBytes, 10))
	}
	q.Set("sig", s.sign(method, ref, contentType, maxBytes, expires.Unix()))
	u := storage.SignedURL{
		URL:       s.baseURL + prefix + string(ref.Bucket) + "/" + ref.Name + "?" + q.Encode(),
		Method:    method,
		ExpiresAt: expires,
	}
	if contentType != "" {
		u.Headers = map[string]string{"Content-Type": contentType}
	}
	return u, nil
}

// Handler serves signed uploads and downloads.
func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /uploads/{bucket}/{object...}", s.handleUpload)
	mux.HandleFunc("GET /files/{bucket}/{object...}", s.handleDownload)
	return mux
}

func (s *Store) handleUpload(w http.ResponseWriter, r *http.Request) {
	ref, exp, ok := s.verify(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	contentType := q.Get("ct")
	maxBytes, err := strconv.ParseInt(q.Get("max"), 10, 64)
	if err != nil || !hmac.Equal([]byte(s.sign(http.MethodPut, ref, contentType, maxBytes, exp)), []byte(q.Get("sig"))) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	if r.Header.Get("Content-Type") != contentType {
		http.Error(w, "content type does not match signed URL", http.StatusBadRequest)
		return
	}
	if r.ContentLength > maxBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	p, err := s.path(ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxBytes)
	err = writeAtomic(p, func(w io.Writer) error {
		_, err := io.Copy(w, body)
		return err
	})
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Store) handleDownload(w http.ResponseWriter, r *http.Request) {
	ref, exp, ok := s.verify(w, r)
	if !ok {
		return
	}
	if !hmac.Equal([]byte(s.sign(http.MethodGet, ref, "", 0, exp)), []byte(r.URL.Query().Get("sig"))) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	p, err := s.path(ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, p)
}

// verify parses the object and checks the expiry shared by both URL kinds.
func (s *Store) verify(w http.ResponseWriter, r *http.Request) (storage.Ref, int64, bool) {
	ref := storage.Ref{Bucket: storage.Bucket(r.PathValue("bucket")), Name: r.PathValue("object")}
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil {
		http.Error(w, "malformed signed URL", http.StatusBadRequest)
		return ref, 0, false
	}
	if s.now().Unix() > exp {
		http.Error(w, "signed URL expired", http.StatusForbidden)
		return ref, 0, false
	}
	return ref, exp, true
}

func (s *Store) path(ref storage.Ref) (string, error) {
	rel := filepath.FromSlash(ref.Name)
	if !slices.Contains(storage.Buckets, ref.Bucket) || ref.Name == "" || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q", storage.ErrInvalidObject, ref)
	}
	return filepath.Join(s.root, string(ref.Bucket), rel), nil
}

func (s *Store) sign(method string, ref storage.Ref, contentType string, maxBytes, exp int64) string {
	mac := hmac.New(sha256.New, s.key)
	fmt.Fprintf(mac, "%s\n%s\n%s\n%d\n%d", method, ref, contentType, maxBytes, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

// writeAtomic writes to a temporary file and renames it into place so a
// failed write never leaves a partial object behind.
func writeAtomic(p string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
