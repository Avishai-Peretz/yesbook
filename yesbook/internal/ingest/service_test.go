package ingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/ingest"
	"github.com/avi-p/yesbook/internal/storage"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type uploads struct {
	sizes   map[string]int64
	signErr error
	signed  []storage.Ref
}

func (u *uploads) SignedUploadURL(_ context.Context, ref storage.Ref, _ string, _ int64, ttl time.Duration) (storage.SignedURL, error) {
	if u.signErr != nil {
		return storage.SignedURL{}, u.signErr
	}
	u.signed = append(u.signed, ref)
	return storage.SignedURL{URL: "https://upload/" + ref.String(), Method: "PUT", ExpiresAt: t0.Add(ttl)}, nil
}

func (u *uploads) Stat(_ context.Context, ref storage.Ref) (storage.ObjectInfo, error) {
	size, ok := u.sizes[ref.String()]
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Size: size}, nil
}

type workflows struct {
	started []string
	err     error
}

func (w *workflows) Start(_ context.Context, _, jobID string) error {
	if w.err != nil {
		return w.err
	}
	w.started = append(w.started, jobID)
	return nil
}

func newService(store *memory.Store, u *uploads, w *workflows) *ingest.Service {
	return ingest.NewService(store, u, w, ingest.WithClock(func() time.Time { return t0 }))
}

var lesson = ingest.RegisterRequest{FileName: "lesson.pdf", ContentType: document.ContentTypePDF, SizeBytes: 1024}

func TestRegisterSignsStagingUpload(t *testing.T) {
	store, u := memory.New(), &uploads{}
	reg, err := newService(store, u, &workflows{}).Register(context.Background(), "tenant-a", lesson)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Job.Status() != document.StatusAwaitingUpload {
		t.Errorf("status = %s, want awaiting_upload", reg.Job.Status())
	}
	if len(u.signed) != 1 || u.signed[0].Bucket != storage.BucketStaging {
		t.Errorf("signed = %v, want one staging object", u.signed)
	}
	if events := store.Events(); len(events) != 1 || events[0].Type != "document.awaiting_upload" {
		t.Errorf("events = %+v, want one awaiting_upload event", events)
	}
}

func TestRegisterSigningFailureLeavesNoJob(t *testing.T) {
	store := memory.New()
	_, err := newService(store, &uploads{signErr: errors.New("no key")}, &workflows{}).Register(context.Background(), "tenant-a", lesson)
	if err == nil {
		t.Fatal("want error")
	}
	if n := len(store.Events()); n != 0 {
		t.Errorf("events = %d, want 0", n)
	}
}

func TestRegisterRejectsInvalidRequest(t *testing.T) {
	req := lesson
	req.ContentType = "image/png"
	_, err := newService(memory.New(), &uploads{}, &workflows{}).Register(context.Background(), "tenant-a", req)
	if !errors.Is(err, document.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestSubmit(t *testing.T) {
	tests := []struct {
		name      string
		uploaded  int64
		startErr  error
		wantErr   error
		wantState document.Status
	}{
		{name: "queues and starts workflow", uploaded: lesson.SizeBytes, wantState: document.StatusQueued},
		{name: "not uploaded", wantErr: ingest.ErrNotUploaded, wantState: document.StatusAwaitingUpload},
		{name: "size mismatch", uploaded: 10, wantErr: ingest.ErrSizeMismatch, wantState: document.StatusAwaitingUpload},
		{name: "workflow start fails", uploaded: lesson.SizeBytes, startErr: errors.New("quota"), wantErr: ingest.ErrStartFailed, wantState: document.StatusQueued},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, u, w := memory.New(), &uploads{sizes: map[string]int64{}}, &workflows{err: tt.startErr}
			svc := newService(store, u, w)
			reg, err := svc.Register(ctx, "tenant-a", lesson)
			if err != nil {
				t.Fatal(err)
			}
			if tt.uploaded > 0 {
				u.sizes[u.signed[0].String()] = tt.uploaded
			}

			_, err = svc.Submit(ctx, "tenant-a", reg.Job.ID(), ingest.SubmitRequest{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			got, err := svc.Get(ctx, "tenant-a", reg.Job.ID())
			if err != nil {
				t.Fatal(err)
			}
			if got.Status() != tt.wantState {
				t.Errorf("status = %s, want %s", got.Status(), tt.wantState)
			}
			if tt.wantErr == nil && len(w.started) != 1 {
				t.Errorf("workflow starts = %d, want 1", len(w.started))
			}
		})
	}
}

func TestSubmitTwiceRestartsWorkflow(t *testing.T) {
	ctx := context.Background()
	store, u, w := memory.New(), &uploads{sizes: map[string]int64{}}, &workflows{}
	svc := newService(store, u, w)
	reg, err := svc.Register(ctx, "tenant-a", lesson)
	if err != nil {
		t.Fatal(err)
	}
	u.sizes[u.signed[0].String()] = lesson.SizeBytes

	for range 2 {
		if _, err := svc.Submit(ctx, "tenant-a", reg.Job.ID(), ingest.SubmitRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.started) != 2 {
		t.Errorf("workflow starts = %d, want 2", len(w.started))
	}
	if n := len(store.Events()); n != 2 {
		t.Errorf("events = %d, want 2 (registered, queued)", n)
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	svc := newService(memory.New(), &uploads{}, &workflows{})
	reg, err := svc.Register(ctx, "tenant-a", lesson)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "tenant-b", reg.Job.ID()); !errors.Is(err, document.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
