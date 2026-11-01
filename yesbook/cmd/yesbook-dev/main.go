// Command yesbook-dev runs the whole system in one process for local
// development: both APIs, signed-URL blob storage on disk, the processing
// pipeline behind a background workflow runner, and the outbox relay
// logging events. It uses PostgreSQL when YESBOOK_DATABASE_URL is set and
// memory otherwise. Extractors beyond PDFium are enabled the same way as in
// the worker (see internal/app).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/localworkflow"
	"github.com/avi-p/yesbook/internal/adapter/logpublisher"
	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/app/extraction"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/processing"
)

const defaultAPIKeys = "dev-tenant:dev-key-0123456789abcdef" //nolint:gosec // published key for local development only

// store is what both the memory and the PostgreSQL adapter provide.
type store interface {
	ingest.JobRepository
	processing.VersionRepository
	processing.AttemptLog
	content.VersionReader
	outbox.Store
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
	port := platform.Env("PORT", "8080")
	keys, err := auth.ParseAPIKeys(platform.Env("YESBOOK_API_KEYS", defaultAPIKeys))
	if err != nil {
		return err
	}

	var st store
	var wake <-chan struct{}
	if url := platform.Env("YESBOOK_DATABASE_URL", ""); url != "" {
		if err := postgres.Migrate(url); err != nil {
			return err
		}
		pool, err := postgres.Connect(ctx, url)
		if err != nil {
			return err
		}
		defer pool.Close()
		pg := postgres.New(pool)
		st, wake = pg, pg.Listen(ctx, log)
		log.InfoContext(ctx, "using postgres")
	} else {
		st = memory.New()
		log.InfoContext(ctx, "using in-memory store; data is lost on exit")
	}

	blobs, err := app.LocalBlobs(platform.Env("YESBOOK_DATA_DIR", "data"), "http://localhost:"+port,
		platform.Env("YESBOOK_LOCAL_SIGNING_KEY", "local-development-signing-key-0123456789"))
	if err != nil {
		return err
	}

	var closers app.Closers
	defer closers.Close()
	pdf, err := pdfium.New(ctx, 2)
	if err != nil {
		return err
	}
	closers.Add(pdf)
	chains, err := extraction.Chains(ctx, pdf, &closers, log)
	if err != nil {
		return err
	}
	cfg, err := extraction.PipelineConfig()
	if err != nil {
		return err
	}
	pipeline, err := processing.NewPipeline(processing.Dependencies{
		Jobs: st, Versions: st, Attempts: st, Objects: blobs, Inspector: pdf,
		Router: processing.NewRouter(chains), Logger: log,
	}, cfg)
	if err != nil {
		return err
	}
	runner := localworkflow.New(pipeline, log)
	defer runner.Close()

	relay := outbox.NewRelay(st, logpublisher.New(log.With("component", "outbox")), log)
	go relay.Run(ctx, wake, 2*time.Second)

	docs := httpapi.NewDocumentHandler(ingest.NewService(st, blobs, runner), keys, log)
	cont := httpapi.NewContentHandler(content.NewService(st, st, blobs, 15*time.Minute), keys, log)
	mux := http.NewServeMux()
	mux.Handle("/uploads/", blobs.Handler())
	mux.Handle("/files/", blobs.Handler())
	mux.Handle("/v1/documents", docs)
	mux.Handle("/v1/documents/", docs)
	mux.Handle("/v1/documents/{id}/content", cont)
	mux.Handle("/v1/versions/", cont)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	log.InfoContext(ctx, "pipeline ready", "pipeline_version", pipeline.Version(), "url", "http://localhost:"+port)
	return platform.Serve(ctx, mux, log)
}
