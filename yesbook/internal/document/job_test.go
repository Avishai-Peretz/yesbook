package document_test

import (
	"errors"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func newJob(t *testing.T) *document.Job {
	t.Helper()
	j, err := document.NewJob("job-1", document.NewJobParams{
		TenantID: "tenant-a", FileName: " lesson.pdf ", ContentType: document.ContentTypePDF, SizeBytes: 10,
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestNewJobValidates(t *testing.T) {
	tests := map[string]document.NewJobParams{
		"missing tenant": {FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: 1},
		"empty name":     {TenantID: "t", FileName: "  ", ContentType: document.ContentTypePDF, SizeBytes: 1},
		"not a pdf":      {TenantID: "t", FileName: "a.doc", ContentType: "application/msword", SizeBytes: 1},
		"empty file":     {TenantID: "t", FileName: "a.pdf", ContentType: document.ContentTypePDF},
		"too large":      {TenantID: "t", FileName: "a.pdf", ContentType: document.ContentTypePDF, SizeBytes: document.MaxSizeBytes + 1},
	}
	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := document.NewJob("id", p, t0); !errors.Is(err, document.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestJobLifecycle(t *testing.T) {
	j := newJob(t)
	if j.FileName() != "lesson.pdf" || j.StagingObject() != "tenant-a/job-1.pdf" {
		t.Fatalf("name = %q, object = %q", j.FileName(), j.StagingObject())
	}
	if err := j.Claim("w1", time.Minute, t0); !errors.Is(err, document.ErrTransition) {
		t.Fatalf("claim before queue: err = %v, want ErrTransition", err)
	}
	if err := j.Queue(t0); err != nil {
		t.Fatal(err)
	}
	if err := j.Claim("w1", time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordHash("abc", t0); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(document.StatusReady, "", "", t0); !errors.Is(err, document.ErrInvalid) {
		t.Errorf("ready without version: err = %v, want ErrInvalid", err)
	}
	if err := j.Complete(document.StatusReady, "v1", "", t0); err != nil {
		t.Fatal(err)
	}
	ev := j.Event("e1", t0)
	if ev.Type != "document.ready" || ev.VersionID != "v1" || ev.Status != document.StatusReady {
		t.Errorf("event = %+v", ev)
	}
	if err := j.Block("late", t0); !errors.Is(err, document.ErrTransition) {
		t.Errorf("block after ready: err = %v, want ErrTransition", err)
	}
}

func TestClaimLease(t *testing.T) {
	j := newJob(t)
	if err := j.Queue(t0); err != nil {
		t.Fatal(err)
	}
	if err := j.Claim("w1", time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if err := j.Claim("w2", time.Minute, t0.Add(30*time.Second)); !errors.Is(err, document.ErrTransition) {
		t.Errorf("claim by other worker within lease: err = %v, want ErrTransition", err)
	}
	if err := j.Claim("w1", time.Minute, t0.Add(30*time.Second)); err != nil {
		t.Errorf("reclaim by same worker: %v", err)
	}
	if err := j.Claim("w2", time.Minute, t0.Add(5*time.Minute)); err != nil {
		t.Errorf("claim after lease expired: %v", err)
	}
}

func TestBudgetExceeded(t *testing.T) {
	b := document.Budget{MaxAttempts: 3, MaxDuration: time.Minute, MaxCostMicros: 100}
	tests := []struct {
		name  string
		spend document.Spend
		now   time.Time
		want  bool
	}{
		{"fresh", document.Spend{}, t0, false},
		{"attempts", document.Spend{Attempts: 3, FirstAttemptAt: t0}, t0, true},
		{"cost", document.Spend{Attempts: 1, CostMicros: 100, FirstAttemptAt: t0}, t0, true},
		{"time", document.Spend{Attempts: 1, FirstAttemptAt: t0}, t0.Add(time.Minute), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := b.Exceeded(tt.spend, tt.now); got != tt.want {
				t.Errorf("Exceeded = %v, want %v", got, tt.want)
			}
		})
	}
}
