// Package httpapi exposes the Document API and the Content API over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/operations"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

const maxRequestBytes = 64 << 10

type Authenticator interface {
	Authenticate(r *http.Request) (tenantID string, err error)
}

type jobView struct {
	ID             string                   `json:"id"`
	Status         document.Status          `json:"status"`
	FileName       string                   `json:"file_name"`
	SizeBytes      int64                    `json:"size_bytes"`
	FileHash       string                   `json:"file_hash,omitempty"`
	VersionID      string                   `json:"version_id,omitempty"`
	Reason         string                   `json:"reason,omitempty"`
	ExtractionMode document.ExtractionMode  `json:"extraction_mode,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}

func viewJob(j *document.Job) jobView {
	return jobView{
		ID: j.ID(), Status: j.Status(), FileName: j.FileName(), SizeBytes: j.SizeBytes(), FileHash: j.FileHash(),
		VersionID: j.VersionID(), Reason: j.Reason(), ExtractionMode: j.ExtractionMode(),
		CreatedAt: j.CreatedAt(), UpdatedAt: j.UpdatedAt(),
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

// decodeOptional accepts an empty body; otherwise behaves like decode.
func decodeOptional(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
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

type documentAPI struct {
	api
	svc *ingest.Service
	ops operations.Reader
}

// NewDocumentHandler serves the Document API. ops supplies tenant-scoped
// listing and timeline reads; it must be non-nil.
func NewDocumentHandler(svc *ingest.Service, ops operations.Reader, a Authenticator, log *slog.Logger) http.Handler {
	h := documentAPI{api: api{auth: a, log: log}, svc: svc, ops: ops}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/documents", h.handle(h.register))
	mux.HandleFunc("GET /v1/documents", h.handle(h.list))
	mux.HandleFunc("GET /v1/documents/{id}", h.handle(h.get))
	mux.HandleFunc("GET /v1/documents/{id}/timeline", h.handle(h.timeline))
	mux.HandleFunc("POST /v1/documents/{id}/submit", h.handle(h.submit))
	mux.HandleFunc("DELETE /v1/documents/{id}", h.handle(h.delete))
	return Middleware(mux, log)
}

func (h documentAPI) register(w http.ResponseWriter, r *http.Request, tenant string) error {
	var req struct {
		FileName       string                  `json:"file_name"`
		ContentType    string                  `json:"content_type"`
		SizeBytes      int64                   `json:"size_bytes"`
		ExtractionMode document.ExtractionMode `json:"extraction_mode"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	reg, err := h.svc.Register(r.Context(), tenant, ingest.RegisterRequest{
		FileName: req.FileName, ContentType: req.ContentType, SizeBytes: req.SizeBytes,
		ExtractionMode: req.ExtractionMode,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, struct {
		Job    jobView           `json:"job"`
		Upload storage.SignedURL `json:"upload"`
	}{viewJob(reg.Job), reg.Upload})
	return nil
}

func (h documentAPI) get(w http.ResponseWriter, r *http.Request, tenant string) error {
	j, err := h.svc.Get(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, viewJob(j))
	return nil
}

func (h documentAPI) list(w http.ResponseWriter, r *http.Request, tenant string) error {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	page, err := h.ops.ListDocuments(r.Context(), operations.ListQuery{
		TenantID: tenant,
		Status:   document.Status(r.URL.Query().Get("status")),
		Cursor:   r.URL.Query().Get("cursor"),
		Limit:    limit,
	})
	if err != nil {
		return err
	}
	docs := make([]jobView, 0, len(page.Documents))
	for _, doc := range page.Documents {
		docs = append(docs, viewDocument(doc))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, struct {
		Documents  []jobView `json:"documents"`
		NextCursor *string   `json:"next_cursor"`
	}{docs, next})
	return nil
}

func (h documentAPI) timeline(w http.ResponseWriter, r *http.Request, tenant string) error {
	tl, err := h.ops.Timeline(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		Document  jobView                    `json:"document"`
		Events    []processing.ProgressEvent `json:"events"`
		Attempts  []attemptView              `json:"attempts"`
		Decisions []document.PathDecision    `json:"decisions,omitempty"`
	}{
		Document:  viewDocument(tl.Document),
		Events:    tl.Events,
		Attempts:  viewAttempts(tl.Attempts),
		Decisions: tl.Decisions,
	})
	return nil
}

func (h documentAPI) submit(w http.ResponseWriter, r *http.Request, tenant string) error {
	var req struct {
		ExtractionMode document.ExtractionMode `json:"extraction_mode"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	j, err := h.svc.Submit(r.Context(), tenant, r.PathValue("id"), ingest.SubmitRequest{
		ExtractionMode: req.ExtractionMode,
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, viewJob(j))
	return nil
}

func (h documentAPI) delete(w http.ResponseWriter, r *http.Request, tenant string) error {
	if err := h.svc.Delete(r.Context(), tenant, r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func viewDocument(d operations.Document) jobView {
	return jobView{
		ID: d.ID, Status: d.Status, FileName: d.FileName, SizeBytes: d.SizeBytes,
		FileHash: d.FileHash, VersionID: d.VersionID, Reason: d.Reason,
		ExtractionMode: d.ExtractionMode,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

type attemptView struct {
	ID               string                    `json:"id"`
	DocumentID       string                    `json:"document_id"`
	TargetID         string                    `json:"target_id"`
	Extractor        string                    `json:"extractor"`
	ExtractorVersion string                    `json:"extractor_version"`
	Attempt          int                       `json:"attempt"`
	Status           string                    `json:"status"`
	CostMicros       int64                     `json:"cost_micros"`
	StartedAt        time.Time                 `json:"started_at"`
	CompletedAt      time.Time                 `json:"completed_at,omitempty"`
	Error            string                    `json:"error,omitempty"`
	ReasonCode       string                    `json:"reason_code,omitempty"`
	ReasonMessage    string                    `json:"reason_message,omitempty"`
	Decision         document.PathDecisionKind `json:"decision,omitempty"`
}

func viewAttempts(attempts []document.Attempt) []attemptView {
	out := make([]attemptView, 0, len(attempts))
	for i, a := range attempts {
		view := attemptView{
			ID:               a.ID,
			DocumentID:       a.JobID,
			TargetID:         a.TargetID,
			Extractor:        a.Extractor,
			ExtractorVersion: a.ExtractorVersion,
			Attempt:          i + 1,
			Status:           attemptStatus(a),
			CostMicros:       a.CostMicros,
			StartedAt:        a.StartedAt,
			Error:            a.Error,
			ReasonCode:       a.ReasonCode,
			ReasonMessage:    a.ReasonMessage,
			Decision:         a.Decision,
		}
		if !a.FinishedAt.IsZero() {
			view.CompletedAt = a.FinishedAt
		}
		out = append(out, view)
	}
	return out
}

func attemptStatus(a document.Attempt) string {
	switch {
	case a.FinishedAt.IsZero():
		return "started"
	case a.Succeeded:
		return "succeeded"
	default:
		return "failed"
	}
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid limit", document.ErrInvalid)
	}
	return n, nil
}

type contentAPI struct {
	api
	svc *content.Service
}

// NewContentHandler serves the Content API.
func NewContentHandler(svc *content.Service, a Authenticator, log *slog.Logger) http.Handler {
	h := contentAPI{api: api{auth: a, log: log}, svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /v1/documents/{id}/content", h.handle(h.forJob))
	mux.HandleFunc("GET /v1/versions/{id}", h.handle(h.version))
	return Middleware(mux, log)
}

func (h contentAPI) forJob(w http.ResponseWriter, r *http.Request, tenant string) error {
	res, err := h.svc.ForJob(r.Context(), tenant, r.PathValue("id"), contentQuery(r))
	if err != nil {
		return err
	}
	writeContent(w, res)
	return nil
}

func (h contentAPI) version(w http.ResponseWriter, r *http.Request, tenant string) error {
	res, err := h.svc.Version(r.Context(), tenant, r.PathValue("id"), contentQuery(r))
	if err != nil {
		return err
	}
	writeContent(w, res)
	return nil
}

func contentQuery(r *http.Request) content.Query {
	return content.Query{IncludeReview: r.URL.Query().Get("include_review") == "true"}
}

func writeContent(w http.ResponseWriter, res content.Result) {
	writeJSON(w, http.StatusOK, struct {
		Content  content.ContentView `json:"content"`
		Original storage.SignedURL   `json:"original"`
	}{res.Content, res.Original})
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
				if !rec.wroteHeader {
					writeError(rec, http.StatusInternalServerError, "internal error")
				}
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
