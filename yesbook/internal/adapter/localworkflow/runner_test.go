package localworkflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

type scripted struct {
	calls int
	fail  int
	seen  chan struct{}
	block chan struct{}
}

func (s *scripted) Process(ctx context.Context, _, _, _ string) (document.Status, error) {
	s.calls++
	if s.seen != nil {
		s.seen <- struct{}{}
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return "", ctx.Err() // status unused on failure
		}
	}
	if s.calls <= s.fail {
		return "", errors.New("temporary")
	}
	return document.StatusReady, nil
}

func (s *scripted) Block(_ context.Context, _, _, _ string) error { return nil }

func TestCloseStopsARunningAttempt(t *testing.T) {
	processor := &scripted{seen: make(chan struct{}), block: make(chan struct{})}
	runner := New(processor, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Start(context.Background(), "tenant", "job"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-processor.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("process did not start")
	}
	done := make(chan struct{})
	go func() {
		runner.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
}

func TestRetriesThenBlocks(t *testing.T) {
	processor := &scripted{fail: defaultAttempts}
	runner := New(processor, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.initial = time.Millisecond
	runner.maxBackoff = 2 * time.Millisecond
	if err := runner.Start(context.Background(), "tenant", "job"); err != nil {
		t.Fatal(err)
	}
	runner.wg.Wait()
	if processor.calls != defaultAttempts {
		t.Fatalf("calls = %d, want %d", processor.calls, defaultAttempts)
	}
}
