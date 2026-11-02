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
	"github.com/avi-p/yesbook/internal/app/modes"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/platform"
)

type config struct {
	DatabaseURL string
	APIKeys     string
	Workflow    string
}

func loadConfig() (config, error) {
	var req platform.RequireEnv
	cfg := config{
		DatabaseURL: req.Get("YESBOOK_DATABASE_URL"),
		APIKeys:     req.Get("YESBOOK_API_KEYS"),
		Workflow:    req.Get("YESBOOK_WORKFLOW"),
	}
	if err := req.Err(); err != nil {
		return config{}, err
	}
	return cfg, nil
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
	log := platform.NewLogger("document-api")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	keys, err := auth.ParseAPIKeys(cfg.APIKeys)
	if err != nil {
		return err
	}

	rt, err := app.OpenRuntime(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer rt.Close()
	exec, err := executions.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create workflows client: %w", err)
	}
	rt.Add(exec)

	store := postgres.New(rt.Pool)
	svc := ingest.NewService(store, rt.Blobs, workflows.New(exec, cfg.Workflow),
		ingest.WithConfiguredModes(modes.Configured(nil)))
	return platform.Serve(ctx, httpapi.NewDocumentHandler(svc, store, keys, log), log)
}
