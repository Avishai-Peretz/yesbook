// Command outbox-publisher relays committed outbox events to Pub/Sub right
// after each commit (LISTEN/NOTIFY) and on a periodic sweep. Cloud Scheduler
// also calls POST /sweep every minute, which covers instances that were
// scaled down. With min-instances=1 and CPU always allocated events go out
// right after commit; scaled to zero, delivery lags by up to a minute.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/internal/adapter/pubsub"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/platform"
)

const (
	sweepInterval = time.Minute
	stuckAfter    = 45 * time.Minute
)

type config struct {
	DatabaseURL string
	Project     string
	Topic       string
}

func loadConfig() (config, error) {
	var req platform.RequireEnv
	cfg := config{
		DatabaseURL: req.Get("YESBOOK_DATABASE_URL"),
		Project:     req.Get("YESBOOK_PROJECT"),
		Topic:       req.Get("YESBOOK_TOPIC"),
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
	log := platform.NewLogger("outbox-publisher")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	client, err := gpubsub.NewClient(ctx, cfg.Project)
	if err != nil {
		return fmt.Errorf("create pubsub client: %w", err)
	}
	defer client.Close()
	publisher := pubsub.New(client, cfg.Topic)
	defer publisher.Stop()

	runCtx, cancel := context.WithCancel(ctx)
	store := postgres.New(pool)
	relay := outbox.NewRelay(store, publisher, log)
	var tasks app.Group
	tasks.Go(func() { relay.Run(runCtx, store.Listen(runCtx, log), sweepInterval) })
	defer func() {
		cancel()
		tasks.Wait()
		store.Wait()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /sweep", func(w http.ResponseWriter, r *http.Request) {
		if err := relay.Sweep(r.Context()); err != nil {
			log.ErrorContext(r.Context(), "scheduled sweep failed", "err", err)
			http.Error(w, "sweep failed", http.StatusInternalServerError)
			return
		}
		reportStuckJobs(r.Context(), store, log)
		w.WriteHeader(http.StatusNoContent)
	})
	return platform.Serve(runCtx, mux, log)
}

// reportStuckJobs logs the count that the stuck-jobs alert is based on.
func reportStuckJobs(ctx context.Context, store *postgres.Store, log *slog.Logger) {
	n, err := store.StuckJobs(ctx, time.Now().Add(-stuckAfter))
	if err != nil {
		log.ErrorContext(ctx, "count stuck jobs failed", "err", err)
		return
	}
	log.InfoContext(ctx, "stuck jobs", "count", n, "older_than_minutes", stuckAfter.Minutes())
}
