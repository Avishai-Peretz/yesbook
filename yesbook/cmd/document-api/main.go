// Command document-api is the Cloud Run service that registers documents,
// issues staging-only upload URLs and starts the processing workflow.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	executions "cloud.google.com/go/workflows/executions/apiv1"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/adapter/workflows"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/platform"
)

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
	log := platform.NewLogger("document-api")
	var req platform.RequireEnv
	dbURL := req.Get("YESBOOK_DATABASE_URL")
	keySpec := req.Get("YESBOOK_API_KEYS")
	workflow := req.Get("YESBOOK_WORKFLOW")
	if err := req.Err(); err != nil {
		return err
	}
	keys, err := auth.ParseAPIKeys(keySpec)
	if err != nil {
		return err
	}

	var closers app.Closers
	defer closers.Close()
	pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	blobs, err := app.GCSBlobs(ctx, &closers)
	if err != nil {
		return err
	}
	exec, err := executions.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create workflows client: %w", err)
	}
	closers.Add(exec)

	svc := ingest.NewService(postgres.New(pool), blobs, workflows.New(exec, workflow))
	return platform.Serve(ctx, httpapi.NewDocumentHandler(svc, keys, log), log)
}
