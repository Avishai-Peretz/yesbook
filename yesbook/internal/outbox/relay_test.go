package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/outbox"
)

type publisher struct {
	got    []string
	failOn string
}

func (p *publisher) Publish(_ context.Context, ev document.Event) error {
	if ev.ID == p.failOn {
		return errors.New("pubsub unavailable")
	}
	p.got = append(p.got, ev.ID)
	return nil
}

func seed(t *testing.T, store *memory.Store, n int) {
	t.Helper()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := range n {
		j, err := document.NewJob(string(rune('a'+i)), document.NewJobParams{
			TenantID: "t", FileName: "f.pdf", ContentType: document.ContentTypePDF, SizeBytes: 1,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateJob(context.Background(), j, j.Event("ev-"+j.ID(), now)); err != nil {
			t.Fatal(err)
		}
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDrainPublishesInOrder(t *testing.T) {
	store, pub := memory.New(), &publisher{}
	seed(t, store, 3)

	n, err := outbox.NewRelay(store, pub, discard()).Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(pub.got) != 3 || pub.got[0] != "ev-a" || pub.got[2] != "ev-c" {
		t.Errorf("published %d: %v", n, pub.got)
	}
	if n, _ := outbox.NewRelay(store, pub, discard()).Drain(context.Background()); n != 0 {
		t.Errorf("second drain published %d, want 0", n)
	}
}

func TestDrainStopsAtFailureAndResumes(t *testing.T) {
	store, pub := memory.New(), &publisher{failOn: "ev-b"}
	seed(t, store, 3)
	relay := outbox.NewRelay(store, pub, discard())

	if _, err := relay.Drain(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if len(pub.got) != 1 {
		t.Fatalf("published = %v, want only ev-a before the failure", pub.got)
	}
	b, err := store.Backlog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Pending != 2 {
		t.Errorf("pending = %d, want 2", b.Pending)
	}

	pub.failOn = ""
	if _, err := relay.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(pub.got) != 3 || pub.got[1] != "ev-b" {
		t.Errorf("published = %v, want ev-b resumed in order", pub.got)
	}
}

func TestRunWakesOnNotification(t *testing.T) {
	store, pub := memory.New(), &publisher{}
	relay := outbox.NewRelay(store, pub, discard())
	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx, wake, time.Hour)
	}()

	seed(t, store, 1)
	wake <- struct{}{}
	wake <- struct{}{} // returns once the first wake-up has been handled
	cancel()
	<-done
	if len(pub.got) != 1 {
		t.Errorf("published = %v, want the event delivered after the wake-up", pub.got)
	}
}
