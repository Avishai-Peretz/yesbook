// Command worker is the Cloud Run job the processing workflow runs:
//
//	worker process TENANT_ID JOB_ID WORKER_ID
//	worker block   TENANT_ID JOB_ID REASON
//
// WORKER_ID should be the workflow execution ID, so a retry of the same
// execution can reclaim the job immediately. A non-zero exit tells the
// workflow to retry; retries resume from saved evidence.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/app/extraction"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/processing"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != 4 || (args[0] != "process" && args[0] != "block") {
		return fmt.Errorf("usage: worker process TENANT_ID JOB_ID WORKER_ID | worker block TENANT_ID JOB_ID REASON")
	}
	cmd, tenantID, jobID := args[0], args[1], args[2]
	log := platform.NewLogger("worker").With("tenant_id", tenantID, "job_id", jobID,
		"execution", platform.Env("CLOUD_RUN_EXECUTION", ""))

	var req platform.RequireEnv
	dbURL := req.Get("YESBOOK_DATABASE_URL")
	if err := req.Err(); err != nil {
		return err
	}
	var closers app.Closers
	defer closers.Close() //nolint:errcheck // best effort on exit
	pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	blobs, err := app.GCSBlobs(ctx, &closers)
	if err != nil {
		return err
	}
	pdf, err := pdfium.New(ctx, 1)
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
	store := postgres.New(pool)
	p, err := processing.NewPipeline(processing.Dependencies{
		Jobs: store, Versions: store, Attempts: store, Objects: blobs, Inspector: pdf,
		Router: processing.NewRouter(chains), Logger: log,
	}, cfg)
	if err != nil {
		return err
	}

	if cmd == "block" {
		return p.Block(ctx, tenantID, jobID, args[3])
	}
	status, err := p.Process(ctx, tenantID, jobID, args[3])
	if err != nil {
		return err
	}
	fmt.Println(status)
	return nil
}
