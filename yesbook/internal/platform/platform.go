// Package platform holds process plumbing shared by the binaries: Cloud
// Logging compatible logs, configuration from the environment and serving
// HTTP on Cloud Run.
package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// NewLogger writes JSON lines that Cloud Logging parses: "severity" and
// "message" instead of slog's "level" and "msg". Every line carries the
// component name.
func NewLogger(component string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			case slog.TimeKey:
				a.Key = "time"
			}
			return a
		},
	})
	return slog.New(h).With("component", component)
}

// Env returns the variable or fallback when it is unset.
func Env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// RequireEnv collects missing required variables so all of them are
// reported at once.
type RequireEnv struct {
	missing []string
}

func (r *RequireEnv) Get(key string) string {
	v := os.Getenv(key)
	if v == "" {
		r.missing = append(r.missing, key)
	}
	return v
}

func (r *RequireEnv) Err() error {
	if len(r.missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required environment variables: %v", r.missing)
}

// EnvInt64 parses an integer variable, returning fallback when it is unset.
func EnvInt64(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// Serve listens on 0.0.0.0:$PORT (8080 by default) until ctx is cancelled,
// then drains in-flight requests within Cloud Run's 10 second grace period.
func Serve(ctx context.Context, h http.Handler, log *slog.Logger) error {
	return serve(ctx, net.JoinHostPort("0.0.0.0", Env("PORT", "8080")), h, log)
}

func serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
