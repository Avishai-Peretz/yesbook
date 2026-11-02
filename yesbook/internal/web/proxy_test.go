package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/web"
)

func TestProxyInjectsCredentialsAndRoutes(t *testing.T) {
	var docAuth, contAuth, docPath, contPath string
	doc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		docAuth = r.Header.Get("Authorization")
		docPath = r.URL.Path
		if r.Header.Get("X-Yesbook-User") != "" || r.Header.Get(auth.HeaderIAPEmail) != "" {
			t.Errorf("client identity headers leaked to document-api")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":"doc"}`))
	}))
	defer doc.Close()
	cont := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contAuth = r.Header.Get("Authorization")
		contPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":"content"}`))
	}))
	defer cont.Close()

	root := writeSPA(t)
	creds, err := auth.ParseBackendCredentials("tenant-a:0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := auth.ParseTenantMap("dev@localhost:tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	h, err := web.New(web.Config{
		DocumentAPI: doc.URL,
		ContentAPI:  cont.URL,
		WebRoot:     root,
		Users:       auth.DevUserAuthenticator{FixedEmail: "dev@localhost"},
		Tenants:     tenants,
		Credentials: creds,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/documents", nil))
	if rec.Code != http.StatusOK || docPath != "/v1/documents" || docAuth != "Bearer 0123456789abcdef" {
		t.Fatalf("document proxy: status=%d path=%q auth=%q body=%s", rec.Code, docPath, docAuth, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/documents/doc-1/content", nil))
	if rec.Code != http.StatusOK || contPath != "/v1/documents/doc-1/content" || contAuth != "Bearer 0123456789abcdef" {
		t.Fatalf("content proxy: status=%d path=%q auth=%q body=%s", rec.Code, contPath, contAuth, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("api healthz = %d", rec.Code)
	}
}

func TestProxyUnauthorized(t *testing.T) {
	doc := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	defer doc.Close()
	root := writeSPA(t)
	creds, _ := auth.ParseBackendCredentials("tenant-a:0123456789abcdef")
	tenants, _ := auth.ParseTenantMap("dev@localhost:tenant-a")
	h, err := web.New(web.Config{
		DocumentAPI: doc.URL,
		ContentAPI:  doc.URL,
		WebRoot:     root,
		Users:       auth.IAPAuthenticator{},
		Tenants:     tenants,
		Credentials: creds,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/documents", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	root := writeSPA(t)
	creds, _ := auth.ParseBackendCredentials("tenant-a:0123456789abcdef")
	tenants, _ := auth.ParseTenantMap("dev@localhost:tenant-a")
	h, err := web.New(web.Config{
		DocumentAPI: "http://127.0.0.1:9",
		ContentAPI:  "http://127.0.0.1:9",
		WebRoot:     root,
		Users:       auth.DevUserAuthenticator{FixedEmail: "dev@localhost"},
		Tenants:     tenants,
		Credentials: creds,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/documents/doc-1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "yesbook-spa") {
		t.Fatalf("spa fallback: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/asset.txt", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "static" {
		t.Fatalf("static file: %d %s", rec.Code, rec.Body.String())
	}
}

func writeSPA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>yesbook-spa</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("static\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
