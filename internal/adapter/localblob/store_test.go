package localblob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/storage"
)

func TestWriteReplacesExistingObject(t *testing.T) {
	s := newTestStore(t)
	ref := storage.Ref{Bucket: storage.BucketStaging, Name: "tenant/doc.pdf"}
	ctx := context.Background()
	if err := s.Write(ctx, ref, "application/pdf", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, ref, "application/pdf", []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(ctx, ref, 100)
	if err != nil || string(got) != "second" {
		t.Fatalf("read = %q, %v", got, err)
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	s := newTestStore(t)
	_, err := s.path(storage.Ref{Bucket: storage.BucketStaging, Name: "../secret"})
	if !errors.Is(err, storage.ErrInvalidObject) {
		t.Fatalf("err = %v", err)
	}
}

func TestPathRejectsSymlinkEscape(t *testing.T) {
	s := newTestStore(t)
	outside := t.TempDir()
	link := filepath.Join(s.root, string(storage.BucketStaging), "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	_, err := s.path(storage.Ref{Bucket: storage.BucketStaging, Name: "escape/secret"})
	if !errors.Is(err, storage.ErrInvalidObject) {
		t.Fatalf("err = %v", err)
	}
}

func TestSignedURLExpiry(t *testing.T) {
	s := newTestStore(t)
	clock := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return clock }
	ref := storage.Ref{Bucket: storage.BucketDerived, Name: "tenant/content.json"}
	signed, err := s.SignedDownloadURL(context.Background(), ref, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u.RequestURI(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWriteAtomicLeavesNoObjectOnFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "object")
	err := writeAtomic(p, func(io.Writer) error { return errors.New("interrupted") })
	if err == nil {
		t.Fatal("want write error")
	}
	if _, statErr := os.Stat(p); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat = %v", statErr)
	}
}

func TestCORSPreflightAndUpload(t *testing.T) {
	s := newTestStore(t)
	s.AllowCORSOrigins("http://localhost:5173")
	ref := storage.Ref{Bucket: storage.BucketStaging, Name: "cors.pdf"}
	signed, err := s.SignedUploadURL(context.Background(), ref, "application/pdf", 1024, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}

	pre := httptest.NewRequest(http.MethodOptions, u.RequestURI(), nil)
	pre.Header.Set("Origin", "http://localhost:5173")
	pre.Header.Set("Access-Control-Request-Method", "PUT")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, pre)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allow-origin = %q", got)
	}

	put := httptest.NewRequest(http.MethodPut, u.RequestURI(), strings.NewReader("%PDF"))
	put.Header.Set("Origin", "http://localhost:5173")
	put.Header.Set("Content-Type", "application/pdf")
	put.ContentLength = 4
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("put allow-origin = %q", got)
	}

	denied := httptest.NewRequest(http.MethodOptions, u.RequestURI(), nil)
	denied.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, denied)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d", rec.Code)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), "http://localhost", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
