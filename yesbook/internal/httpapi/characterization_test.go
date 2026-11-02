package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/httpapi"
)

func TestMalformedAndTrailingJSON(t *testing.T) {
	e := newEnv(t)
	body := `{"file_name":"lesson.pdf","content_type":"application/pdf","size_bytes":100}`
	if status := call(t, http.MethodPost, e.docs.URL+"/v1/documents", keyA, []byte("{"), nil, nil); status != http.StatusBadRequest {
		t.Fatalf("malformed status = %d", status)
	}
	if status := call(t, http.MethodPost, e.docs.URL+"/v1/documents", keyA, []byte(body+`{"extra":true}`), nil, nil); status != http.StatusCreated {
		t.Fatalf("trailing status = %d", status)
	}
}

func TestPanicRecovery(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	before := httpapi.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("before headers")
	}), log)
	rec := httptest.NewRecorder()
	before.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/documents", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("before = %d %s", rec.Code, rec.Body.String())
	}

	after := httpapi.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		panic("after headers")
	}), log)
	rec = httptest.NewRecorder()
	after.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/documents", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("after = %d", rec.Code)
	}
}
