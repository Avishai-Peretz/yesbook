// Command yesbook-dev runs the whole system in one process for local
// development: both APIs, signed-URL blob storage on disk, the processing
// pipeline behind a background workflow runner, and the outbox relay
// logging events. It uses PostgreSQL when YESBOOK_DATABASE_URL is set and
// memory otherwise. Docling defaults to http://localhost:5001 (start it with
// make dev / docker compose up docling). Other extractors use the same env
// vars as the worker (see internal/app).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/localblob"
	"github.com/avi-p/yesbook/internal/adapter/localworkflow"
	"github.com/avi-p/yesbook/internal/adapter/logpublisher"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/app/extraction"
	"github.com/avi-p/yesbook/internal/app/modes"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/operations"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/processing"
)

const defaultAPIKeys = "dev-tenant:dev-key-0123456789abcdef" //nolint:gosec // published key for local development only

// store is what both the memory and the PostgreSQL adapter provide.
type store interface {
	ingest.JobRepository
	ingest.DocumentDeleter
	processing.VersionRepository
	processing.AttemptLog
	content.VersionReader
	outbox.Store
	processing.ProgressSink
	operations.Reader
}

type devConfig struct {
	Port              string
	APIKeys           string
	DatabaseURL       string
	DataDir           string
	SigningKey        string
	UploadCORSOrigins string
}

func loadConfig() devConfig {
	return devConfig{
		Port:        platform.Env("PORT", "8080"),
		APIKeys:     platform.Env("YESBOOK_API_KEYS", defaultAPIKeys),
		DatabaseURL: platform.Env("YESBOOK_DATABASE_URL", ""),
		DataDir:     platform.Env("YESBOOK_DATA_DIR", "data"),
		SigningKey:  platform.Env("YESBOOK_LOCAL_SIGNING_KEY", "local-development-signing-key-0123456789"),
		// Vite default origin; override or clear via YESBOOK_UPLOAD_CORS_ORIGINS.
		UploadCORSOrigins: platform.Env("YESBOOK_UPLOAD_CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173"),
	}
}

type backend struct {
	store store
	wake  <-chan struct{}
	close func()
	wait  func()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log := platform.NewLogger("yesbook-dev")
	cfg := loadConfig()
	// Docling is part of the default local path; make dev also starts the container.
	if os.Getenv("YESBOOK_DOCLING_URL") == "" {
		_ = os.Setenv("YESBOOK_DOCLING_URL", "http://localhost:5001")
	}
	keys, err := auth.ParseAPIKeys(cfg.APIKeys)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	opened, err := openBackend(runCtx, cfg.DatabaseURL, log)
	if err != nil {
		cancel()
		return err
	}
	defer opened.close()
	stop := func() {
		cancel()
		opened.wait()
	}

	blobs, err := app.LocalBlobs(cfg.DataDir, "http://localhost:"+cfg.Port, cfg.SigningKey)
	if err != nil {
		stop()
		return err
	}
	if origins := splitCSV(cfg.UploadCORSOrigins); len(origins) > 0 {
		blobs.AllowCORSOrigins(origins...)
	}
	var closers app.Closers
	defer closers.Close()
	pdf, err := pdfium.New(runCtx, 2)
	if err != nil {
		stop()
		return err
	}
	closers.Add(pdf)
	pipeline, err := extraction.BuildPipeline(runCtx, pdf, &closers, processing.Dependencies{
		Jobs: opened.store, Versions: opened.store, Attempts: opened.store, Progress: opened.store,
		Objects: blobs, Logger: log,
	})
	if err != nil {
		stop()
		return err
	}
	runner := localworkflow.New(pipeline, log)
	defer runner.Close()

	relay := outbox.NewRelay(opened.store, logpublisher.New(log.With("component", "outbox")), log)
	var tasks app.Group
	tasks.Go(func() { relay.Run(runCtx, opened.wake, 2*time.Second) })
	defer func() {
		cancel()
		tasks.Wait()
		opened.wait()
	}()

	log.InfoContext(runCtx, "pipeline ready", "pipeline_version", pipeline.Version(), "url", "http://localhost:"+cfg.Port)
	return platform.Serve(runCtx, routes(blobs, opened.store, runner, keys, log), log)
}

func openBackend(ctx context.Context, databaseURL string, log *slog.Logger) (backend, error) {
	if databaseURL == "" {
		log.InfoContext(ctx, "using in-memory store; data is lost on exit")
		return backend{store: memory.New(), close: func() {}, wait: func() {}}, nil
	}
	if err := postgres.Migrate(databaseURL); err != nil {
		return backend{}, err
	}
	pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return backend{}, err
	}
	pg := postgres.New(pool)
	log.InfoContext(ctx, "using postgres")
	return backend{
		store: pg,
		wake:  pg.Listen(ctx, log),
		close: pool.Close,
		wait:  pg.Wait,
	}, nil
}

func routes(blobs *localblob.Store, st store, runner *localworkflow.Runner, keys *auth.APIKeys, log *slog.Logger) http.Handler {
	docs := httpapi.NewDocumentHandler(ingest.NewService(st, blobs, runner, ingest.WithConfiguredModes(modes.Configured(nil))), st, keys, log)
	cont := httpapi.NewContentHandler(content.NewService(st, st, blobs, 15*time.Minute), keys, log)
	mux := http.NewServeMux()
	mux.Handle("/uploads/", blobs.Handler())
	mux.Handle("/files/", blobs.Handler())
	mux.Handle("/v1/documents", docs)
	mux.Handle("/v1/documents/", docs)
	mux.Handle("/v1/documents/{id}/content", cont)
	mux.Handle("/v1/versions/", cont)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return mux
}

func splitCSV(spec string) []string {
	var out []string
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
