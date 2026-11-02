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

type config struct {
	DatabaseURL string
	APIKeys     string
}

func loadConfig() (config, error) {
	var req platform.RequireEnv
	cfg := config{DatabaseURL: req.Get("YESBOOK_DATABASE_URL"), APIKeys: req.Get("YESBOOK_API_KEYS")}
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
	log := platform.NewLogger("content-api")
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

	store := postgres.New(rt.Pool)
	svc := content.NewService(store, store, rt.Blobs, 15*time.Minute)
	return platform.Serve(ctx, httpapi.NewContentHandler(svc, keys, log), log)
}
