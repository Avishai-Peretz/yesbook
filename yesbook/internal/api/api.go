// Package api implements the Document API.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/blob"
	"github.com/avi-p/yesbook/internal/id"
	"github.com/avi-p/yesbook/internal/job"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/store"
	"github.com/avi-p/yesbook/internal/workflow"
)

const maxRequestBytes = 1 << 20

type Config struct {
	Store     store.Store
	Blobs     blob.Store
	Auth      auth.Authenticator
	Workflows workflow.Starter
	Logger    *slog.Logger
	UploadTTL time.Duration
	Now       func() time.Time
	NewID     func() string
}

type Server struct {
	cfg Config
}

func New(cfg Config) *Server {
	if cfg.UploadTTL == 0 {
		cfg.UploadTTL = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = id.New
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/documents", s.authed(s.createDocument))
	mux.HandleFunc("GET /v1/documents/{id}", s.authed(s.getDocument))
	mux.HandleFunc("POST /v1/documents/{id}/submit", s.authed(s.submitDocument))
}

type tenantHandler func(w http.ResponseWriter, r *http.Request, tenantID string)

func (s *Server) authed(h tenantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := s.cfg.Auth.Authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		h(w, r, tenantID)
	}
}

type createRequest struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type createResponse struct {
	Job             job.Job   `json:"job"`
	UploadURL       string    `json:"upload_url"`
	UploadExpiresAt time.Time `json:"upload_expires_at"`
}

func (s *Server) createDocument(w http.ResponseWriter, r *http.Request, tenantID string) {
	ctx := r.Context()
	var req createRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	now := s.cfg.Now().UTC()
	j, err := job.New(s.cfg.NewID(), job.NewRequest{
		TenantID:    tenantID,
		FileName:    req.FileName,
		ContentType: req.ContentType,
		SizeBytes:   req.SizeBytes,
	}, now)
	if errors.Is(err, job.ErrInvalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.internalError(w, r, "build job", err)
		return
	}

	upload, err := s.cfg.Blobs.SignedUploadURL(ctx, j.StagingObject, j.ContentType, j.SizeBytes, s.cfg.UploadTTL)
	if err != nil {
		s.internalError(w, r, "sign upload URL", err)
		return
	}

	ev := outbox.ForJob(s.cfg.NewID(), outbox.TypeJobRegistered, j, now)
	if err := s.cfg.Store.CreateJob(ctx, j, ev); err != nil {
		s.internalError(w, r, "create job", err)
		return
	}

	s.cfg.Logger.InfoContext(ctx, "job registered", "tenant_id", tenantID, "job_id", j.ID)
	writeJSON(w, http.StatusCreated, createResponse{Job: j, UploadURL: upload.URL, UploadExpiresAt: upload.ExpiresAt})
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request, tenantID string) {
	j, err := s.cfg.Store.GetJob(r.Context(), tenantID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "get job", err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// submitDocument confirms the upload and starts processing. Submitting a job
// that is already queued restarts the workflow, so clients can retry after a
// failed start.
func (s *Server) submitDocument(w http.ResponseWriter, r *http.Request, tenantID string) {
	ctx := r.Context()
	jobID := r.PathValue("id")

	j, err := s.cfg.Store.GetJob(ctx, tenantID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		s.internalError(w, r, "get job", err)
		return
	}

	if j.Status == job.StatusAwaitingUpload {
		info, err := s.cfg.Blobs.Stat(ctx, j.StagingObject)
		if errors.Is(err, blob.ErrNotFound) {
			writeError(w, http.StatusConflict, "file has not been uploaded")
			return
		}
		if err != nil {
			s.internalError(w, r, "stat upload", err)
			return
		}
		if info.Size != j.SizeBytes {
			writeError(w, http.StatusConflict,
				fmt.Sprintf("uploaded size %d does not match declared size %d", info.Size, j.SizeBytes))
			return
		}

		now := s.cfg.Now().UTC()
		queued := j
		queued.Status = job.StatusQueued
		ev := outbox.ForJob(s.cfg.NewID(), outbox.TypeJobQueued, queued, now)
		j, err = s.cfg.Store.TransitionJob(ctx, tenantID, jobID, job.StatusAwaitingUpload, job.StatusQueued, now, ev)
		if errors.Is(err, store.ErrConflict) {
			j, err = s.cfg.Store.GetJob(ctx, tenantID, jobID)
		}
		if err != nil {
			s.internalError(w, r, "queue job", err)
			return
		}
	}

	if j.Status != job.StatusQueued {
		writeError(w, http.StatusConflict, "document is already "+string(j.Status))
		return
	}

	if err := s.cfg.Workflows.Start(ctx, tenantID, jobID); err != nil {
		s.cfg.Logger.ErrorContext(ctx, "start workflow", "tenant_id", tenantID, "job_id", jobID, "err", err)
		writeError(w, http.StatusBadGateway, "could not start processing; retry submit")
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.cfg.Logger.ErrorContext(r.Context(), op, "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
