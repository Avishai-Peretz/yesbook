// Command content-api is the Cloud Run service education services call for
// accepted content and signed URLs, within their tenant.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/content"
	"github.com/avi-p/yesbook/internal/httpapi"
	"github.com/avi-p/yesbook/internal/platform"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log := platform.NewLogger("content-api")
	var req platform.RequireEnv
	dbURL := req.Get("YESBOOK_DATABASE_URL")
	keySpec := req.Get("YESBOOK_API_KEYS")
	if err := req.Err(); err != nil {
		return err
	}
	keys, err := auth.ParseAPIKeys(keySpec)
	if err != nil {
		return err
	}

	var closers app.Closers
	defer closers.Close() //nolint:errcheck // best effort on shutdown
	pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	blobs, err := app.GCSBlobs(ctx, &closers)
	if err != nil {
		return err
	}

	store := postgres.New(pool)
	svc := content.NewService(store, store, blobs, 15*time.Minute)
	return platform.Serve(ctx, httpapi.NewContentHandler(svc, keys, log), log)
}
