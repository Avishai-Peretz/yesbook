package platform

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEnvAndRequire(t *testing.T) {
	t.Setenv("YESBOOK_PRESENT", "value")
	t.Setenv("YESBOOK_EMPTY", "")
	if got := Env("YESBOOK_PRESENT", "fallback"); got != "value" {
		t.Fatalf("Env present = %q", got)
	}
	if got := Env("YESBOOK_EMPTY", "fallback"); got != "fallback" {
		t.Fatalf("Env empty = %q", got)
	}
	if got := Env("YESBOOK_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("Env missing = %q", got)
	}

	var req RequireEnv
	if req.Get("YESBOOK_PRESENT") != "value" || req.Err() != nil {
		t.Fatal("present variable should satisfy RequireEnv")
	}
	req.Get("YESBOOK_MISSING_REQUIRED")
	err := req.Err()
	if err == nil || !strings.Contains(err.Error(), "YESBOOK_MISSING_REQUIRED") {
		t.Fatal(err)
	}
}

func TestEnvInt64(t *testing.T) {
	t.Setenv("YESBOOK_INT", "")
	n, err := EnvInt64("YESBOOK_INT", 7)
	if err != nil || n != 7 {
		t.Fatalf("fallback = %d, %v", n, err)
	}
	t.Setenv("YESBOOK_INT", "12")
	n, err = EnvInt64("YESBOOK_INT", 7)
	if err != nil || n != 12 {
		t.Fatalf("parsed = %d, %v", n, err)
	}
	t.Setenv("YESBOOK_INT", "nope")
	if _, err = EnvInt64("YESBOOK_INT", 7); err == nil {
		t.Fatal("want parse error")
	}
}

func TestServeShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- serve(ctx, ln.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return")
	}
}
