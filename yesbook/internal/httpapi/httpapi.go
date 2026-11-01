// Package httpapi exposes the Document API and the Content API over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/storage"
)

const maxRequestBytes = 64 << 10

type Authenticator interface {
	Authenticate(r *http.Request) (tenantID string, err error)
}

type jobView struct {
	ID        string          `json:"id"`
	Status    document.Status `json:"status"`
	FileName  string          `json:"file_name"`
	SizeBytes int64           `json:"size_bytes"`
	FileHash  string          `json:"file_hash,omitempty"`
	VersionID string          `json:"version_id,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func viewJob(j *document.Job) jobView {
	return jobView{
		ID: j.ID(), Status: j.Status(), FileName: j.FileName(), SizeBytes: j.SizeBytes(), FileHash: j.FileHash(),
		VersionID: j.VersionID(), Reason: j.Reason(), CreatedAt: j.CreatedAt(), UpdatedAt: j.UpdatedAt(),
	}
}

// tenantHandler is an authenticated handler.
type tenantHandler func(w http.ResponseWriter, r *http.Request, tenantID string) error

type api struct {
	auth Authenticator
	log  *slog.Logger
}

// handle authenticates the request and turns returned errors into HTTP
// responses. Only unexpected errors are logged, once, here.
func (a api) handle(h tenantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant, err := a.auth.Authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if err := h(w, r, tenant); err != nil {
			status, msg := classify(err)
			if status == http.StatusInternalServerError || status == http.StatusServiceUnavailable {
				a.log.ErrorContext(r.Context(), "request failed", "tenant_id", tenant, "path", r.URL.Path, "err", err)
			}
			writeError(w, status, msg)
		}
	}
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, document.ErrInvalid), errors.Is(err, errBadRequest):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, document.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, ingest.ErrNotUploaded), errors.Is(err, ingest.ErrSizeMismatch),
		errors.Is(err, ingest.ErrNotSubmittable), errors.Is(err, document.ErrTransition),
		errors.Is(err, content.ErrNotPublished), errors.Is(err, content.ErrQualityGate):
		return http.StatusConflict, err.Error()
	case errors.Is(err, ingest.ErrStartFailed):
		return http.StatusServiceUnavailable, "processing could not be started, retry the submit"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

var errBadRequest = errors.New("bad request")

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.Join(errBadRequest, err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// NewDocumentHandler serves the Document API.
func NewDocumentHandler(svc *ingest.Service, a Authenticator, log *slog.Logger) http.Handler {
	h := api{auth: a, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/documents", h.handle(func(w http.ResponseWriter, r *http.Request, tenant string) error {
		var req struct {
			FileName    string `json:"file_name"`
			ContentType string `json:"content_type"`
			SizeBytes   int64  `json:"size_bytes"`
		}
		if err := decode(r, &req); err != nil {
			return err
		}
		reg, err := svc.Register(r.Context(), tenant, ingest.RegisterRequest{
			FileName: req.FileName, ContentType: req.ContentType, SizeBytes: req.SizeBytes,
		})
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, struct {
			Job    jobView           `json:"job"`
			Upload storage.SignedURL `json:"upload"`
		}{viewJob(reg.Job), reg.Upload})
		return nil
	}))
	mux.HandleFunc("GET /v1/documents/{id}", h.handle(func(w http.ResponseWriter, r *http.Request, tenant string) error {
		j, err := svc.Get(r.Context(), tenant, r.PathValue("id"))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, viewJob(j))
		return nil
	}))
	mux.HandleFunc("POST /v1/documents/{id}/submit", h.handle(func(w http.ResponseWriter, r *http.Request, tenant string) error {
		j, err := svc.Submit(r.Context(), tenant, r.PathValue("id"))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusAccepted, viewJob(j))
		return nil
	}))
	return Middleware(mux, log)
}

// NewContentHandler serves the Content API.
func NewContentHandler(svc *content.Service, a Authenticator, log *slog.Logger) http.Handler {
	h := api{auth: a, log: log}
	respond := func(w http.ResponseWriter, res content.Result) {
		writeJSON(w, http.StatusOK, struct {
			Content  document.Content  `json:"content"`
			Original storage.SignedURL `json:"original"`
		}{res.Content, res.Original})
	}
	query := func(r *http.Request) content.Query {
		return content.Query{IncludeReview: r.URL.Query().Get("include_review") == "true"}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /v1/documents/{id}/content", h.handle(func(w http.ResponseWriter, r *http.Request, tenant string) error {
		res, err := svc.ForJob(r.Context(), tenant, r.PathValue("id"), query(r))
		if err != nil {
			return err
		}
		respond(w, res)
		return nil
	}))
	mux.HandleFunc("GET /v1/versions/{id}", h.handle(func(w http.ResponseWriter, r *http.Request, tenant string) error {
		res, err := svc.Version(r.Context(), tenant, r.PathValue("id"), query(r))
		if err != nil {
			return err
		}
		respond(w, res)
		return nil
	}))
	return Middleware(mux, log)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Middleware recovers panics and logs one line per request with the Cloud
// Run trace ID, so logs of a request can be correlated.
func Middleware(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		trace, _, _ := strings.Cut(r.Header.Get("X-Cloud-Trace-Context"), "/")
		defer func() {
			if p := recover(); p != nil {
				log.ErrorContext(r.Context(), "panic serving request", "panic", p, "path", r.URL.Path, "trace", trace)
				writeError(rec, http.StatusInternalServerError, "internal error")
			}
			if r.URL.Path == "/healthz" {
				return
			}
			log.InfoContext(r.Context(), "request",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(), "trace", trace)
		}()
		next.ServeHTTP(rec, r)
	})
}
