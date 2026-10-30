package app

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type recordedCloser struct {
	name  string
	order *[]string
	err   error
}

func (c recordedCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}

func TestClosersCloseInReverseOrder(t *testing.T) {
	var order []string
	var closers Closers
	closers.Add(recordedCloser{name: "first", order: &order})
	closers.Add(recordedCloser{name: "second", order: &order, err: errors.New("boom")})
	err := closers.Close()
	if err == nil || order[0] != "second" || order[1] != "first" {
		t.Fatalf("order = %v, err = %v", order, err)
	}
}

func TestOpenRuntimeRejectsMalformedDatabaseURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := OpenRuntime(ctx, "://"); err == nil {
		t.Fatal("malformed database URL should fail before opening storage")
	}
}

func TestGroupWaitsForGoroutines(t *testing.T) {
	var g Group
	done := make(chan struct{})
	g.Go(func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	})
	g.Wait()
	select {
	case <-done:
	default:
		t.Fatal("Wait returned before the goroutine finished")
	}
}

var _ io.Closer = recordedCloser{}
