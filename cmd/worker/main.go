// Command worker is the Cloud Run job the processing workflow runs:
//
//	worker process TENANT_ID JOB_ID WORKER_ID
//	worker block   TENANT_ID JOB_ID WORKER_ID REASON
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
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	switch {
	case len(args) == 4 && args[0] == "process":
	case len(args) == 5 && args[0] == "block":
	default:
		return fmt.Errorf("usage: worker process TENANT_ID JOB_ID WORKER_ID | worker block TENANT_ID JOB_ID WORKER_ID REASON")
	}
	cmd, tenantID, jobID := args[0], args[1], args[2]
	log := platform.NewLogger("worker").With("tenant_id", tenantID, "job_id", jobID,
		"execution", platform.Env("CLOUD_RUN_EXECUTION", ""))
	var req platform.RequireEnv
	dbURL := req.Get("YESBOOK_DATABASE_URL")
	if err := req.Err(); err != nil {
		return err
	}

	rt, err := app.OpenRuntime(ctx, dbURL)
	if err != nil {
		return err
	}
	defer rt.Close()
	pdf, err := pdfium.New(ctx, 1)
	if err != nil {
		return err
	}
	rt.Add(pdf)
	store := postgres.New(rt.Pool)
	p, err := extraction.BuildPipeline(ctx, pdf, rt, processing.Dependencies{
		Jobs: store, Versions: store, Attempts: store, Progress: store, Objects: rt.Blobs, Logger: log,
	})
	if err != nil {
		return err
	}
	if cmd == "block" {
		return p.Block(ctx, tenantID, jobID, args[3], args[4])
	}
	status, err := p.Process(processing.WithRunIdentity(ctx, processing.RunIdentity{
		RunID: platform.Env("CLOUD_RUN_EXECUTION", ""),
	}), tenantID, jobID, args[3])
	if err != nil {
		return err
	}
	fmt.Println(status)
	return nil
}
