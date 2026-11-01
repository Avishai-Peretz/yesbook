package blob

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
	"strconv"
	"time"
)

// Local stores objects on disk and emulates Cloud Storage signed URLs with
// HMAC-signed query parameters. Register HandleUpload on "PUT /uploads/{object...}".
type Local struct {
	root    string
	baseURL string
	key     []byte
	now     func() time.Time
}

func NewLocal(root, baseURL string, key []byte) (*Local, error) {
	if len(key) < 32 {
		return nil, errors.New("signing key must be at least 32 bytes")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Local{root: root, baseURL: baseURL, key: key, now: time.Now}, nil
}

func (l *Local) SignedUploadURL(_ context.Context, object, contentType string, maxBytes int64, ttl time.Duration) (UploadURL, error) {
	if _, err := l.path(object); err != nil {
		return UploadURL{}, err
	}
	expires := l.now().Add(ttl).Truncate(time.Second)
	q := url.Values{}
	q.Set("ct", contentType)
	q.Set("max", strconv.FormatInt(maxBytes, 10))
	q.Set("exp", strconv.FormatInt(expires.Unix(), 10))
	q.Set("sig", l.sign(object, contentType, maxBytes, expires.Unix()))
	return UploadURL{
		URL:       l.baseURL + "/uploads/" + object + "?" + q.Encode(),
		ExpiresAt: expires,
	}, nil
}

func (l *Local) Stat(_ context.Context, object string) (ObjectInfo, error) {
	p, err := l.path(object)
	if err != nil {
		return ObjectInfo{}, err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return ObjectInfo{}, ErrNotFound
	}
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}
	return ObjectInfo{Size: fi.Size()}, nil
}

func (l *Local) HandleUpload(w http.ResponseWriter, r *http.Request) {
	object := r.PathValue("object")
	q := r.URL.Query()
	contentType := q.Get("ct")
	maxBytes, err1 := strconv.ParseInt(q.Get("max"), 10, 64)
	exp, err2 := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, "malformed signed URL", http.StatusBadRequest)
		return
	}
	want := l.sign(object, contentType, maxBytes, exp)
	if !hmac.Equal([]byte(want), []byte(q.Get("sig"))) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	if l.now().Unix() > exp {
		http.Error(w, "signed URL expired", http.StatusForbidden)
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
	p, err := l.path(object)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := l.write(p, http.MaxBytesReader(w, r.Body, maxBytes)); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// write streams body to a temporary file and renames it into place so a
// failed upload never leaves a partial object behind.
func (l *Local) write(p string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (l *Local) path(object string) (string, error) {
	rel := filepath.FromSlash(object)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("invalid object name %q", object)
	}
	return filepath.Join(l.root, rel), nil
}

func (l *Local) sign(object, contentType string, maxBytes, exp int64) string {
	mac := hmac.New(sha256.New, l.key)
	fmt.Fprintf(mac, "%s\n%s\n%d\n%d", object, contentType, maxBytes, exp)
	return hex.EncodeToString(mac.Sum(nil))
}
