// Command outbox-publisher relays committed outbox events to Pub/Sub right
// after each commit (LISTEN/NOTIFY) and on a periodic sweep. Cloud Scheduler
// also calls POST /sweep every minute, which covers instances that were
// scaled down. Run it with min-instances=1 and CPU always allocated.
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
	"github.com/avi-p/yesbook/internal/outbox"
	"github.com/avi-p/yesbook/internal/platform"
)

const (
	sweepInterval = time.Minute
	stuckAfter    = 45 * time.Minute
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
	log := platform.NewLogger("outbox-publisher")
	var req platform.RequireEnv
	dbURL := req.Get("YESBOOK_DATABASE_URL")
	project := req.Get("YESBOOK_PROJECT")
	topic := req.Get("YESBOOK_TOPIC")
	if err := req.Err(); err != nil {
		return err
	}

	pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	client, err := gpubsub.NewClient(ctx, project)
	if err != nil {
		return fmt.Errorf("create pubsub client: %w", err)
	}
	defer client.Close() //nolint:errcheck // best effort on shutdown
	publisher := pubsub.New(client, topic)
	defer publisher.Stop()

	store := postgres.New(pool)
	relay := outbox.NewRelay(store, publisher, log)
	go relay.Run(ctx, store.Listen(ctx, log), sweepInterval)

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
	return platform.Serve(ctx, mux, log)
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
