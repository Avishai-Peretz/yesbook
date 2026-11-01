// Command document-api serves the Yesbook Document API.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avi-p/yesbook/internal/api"
	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/blob"
	"github.com/avi-p/yesbook/internal/store"
	"github.com/avi-p/yesbook/internal/workflow"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("document-api stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := cmp.Or(os.Getenv("YESBOOK_ADDR"), ":8080")
	publicURL := cmp.Or(os.Getenv("YESBOOK_PUBLIC_URL"), "http://localhost:8080")
	uploadDir := cmp.Or(os.Getenv("YESBOOK_UPLOAD_DIR"), "data")

	apiKeys := os.Getenv("YESBOOK_API_KEYS")
	if apiKeys == "" {
		return errors.New("YESBOOK_API_KEYS is required, for example dev-key:tenant-dev")
	}
	authn, err := auth.ParseStaticKeys(apiKeys)
	if err != nil {
		return err
	}

	signingKey := []byte(os.Getenv("YESBOOK_SIGNING_KEY"))
	if len(signingKey) == 0 {
		logger.Warn("YESBOOK_SIGNING_KEY not set; using a random key, upload URLs stop working after restart")
		signingKey = make([]byte, 32)
		rand.Read(signingKey)
	}
	blobs, err := blob.NewLocal(uploadDir, publicURL, signingKey)
	if err != nil {
		return err
	}

	var st store.Store
	if dbURL := os.Getenv("YESBOOK_DATABASE_URL"); dbURL != "" {
		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		defer pool.Close()
		if err := store.Migrate(ctx, pool); err != nil {
			return err
		}
		st = store.NewPostgres(pool)
		logger.Info("using PostgreSQL store")
	} else {
		logger.Warn("YESBOOK_DATABASE_URL not set; using in-memory store, data is lost on restart")
		st = store.NewMemory()
	}

	mux := http.NewServeMux()
	api.New(api.Config{
		Store:     st,
		Blobs:     blobs,
		Auth:      authn,
		Workflows: workflow.LogStarter{Log: logger},
		Logger:    logger,
	}).Register(mux)
	mux.HandleFunc("PUT /uploads/{object...}", blobs.HandleUpload)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
