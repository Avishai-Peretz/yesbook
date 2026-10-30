package processing_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

type wantEvent struct {
	stage      processing.ProgressStage
	status     processing.ProgressStatus
	reason     string
	reasonCode string
	current    int
	total      int
	unit       string
}

func step(stage processing.ProgressStage, status processing.ProgressStatus, reason string) wantEvent {
	return wantEvent{stage: stage, status: status, reason: reason}
}

func stepCode(stage processing.ProgressStage, status processing.ProgressStatus, reason, reasonCode string) wantEvent {
	return wantEvent{stage: stage, status: status, reason: reason, reasonCode: reasonCode}
}

func advance(stage processing.ProgressStage, current, total int, unit, reason string) wantEvent {
	return wantEvent{
		stage: stage, status: processing.ProgressAdvancing, reason: reason,
		current: current, total: total, unit: unit,
	}
}

func assertEvents(
	t *testing.T,
	jobID, workflow, runID string,
	retry int,
	events []processing.ProgressEvent,
	want []wantEvent,
) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("progress =\n%swant =\n%s", formatEvents(events), formatWant(want))
	}
	var prev int64
	seen := map[string]bool{}
	gotRun := runID
	for i, ev := range events {
		w := want[i]
		if ev.Sequence <= prev {
			t.Fatalf("sequence went from %d to %d", prev, ev.Sequence)
		}
		prev = ev.Sequence
		if ev.ID == "" || seen[ev.ID] {
			t.Fatalf("event id %q", ev.ID)
		}
		seen[ev.ID] = true
		if ev.TenantID != tenant || ev.JobID != jobID {
			t.Fatalf("event %d owner = %s/%s", i, ev.TenantID, ev.JobID)
		}
		if ev.WorkflowID != workflow || ev.Retry != retry {
			t.Fatalf("event %d identity = %s retry %d", i, ev.WorkflowID, ev.Retry)
		}
		if gotRun == "" {
			gotRun = ev.RunID
		}
		if ev.RunID == "" || ev.RunID != gotRun {
			t.Fatalf("event %d run = %q, want %q", i, ev.RunID, gotRun)
		}
		if !ev.OccurredAt.Equal(t0) {
			t.Fatalf("event %d occurred at %s", i, ev.OccurredAt)
		}
		if ev.Stage != w.stage || ev.Status != w.status || ev.Reason != w.reason {
			t.Fatalf("event %d = %s %s %q, want %s %s %q", i, ev.Stage, ev.Status, ev.Reason, w.stage, w.status, w.reason)
		}
		if ev.ReasonCode != w.reasonCode {
			t.Fatalf("event %d reason_code = %q, want %q", i, ev.ReasonCode, w.reasonCode)
		}
		if w.reasonCode != "" && ev.ReasonMessage != w.reason {
			t.Fatalf("event %d reason_message = %q, want %q", i, ev.ReasonMessage, w.reason)
		}
		if w.status == processing.ProgressAdvancing {
			if ev.Progress == nil || ev.Progress.Current != w.current || ev.Progress.Total != w.total || ev.Progress.Unit != w.unit {
				got := "nil"
				if ev.Progress != nil {
					got = fmt.Sprintf("%d/%d %s", ev.Progress.Current, ev.Progress.Total, ev.Progress.Unit)
				}
				t.Fatalf("event %d progress = %s, want %d/%d %s", i, got, w.current, w.total, w.unit)
			}
			continue
		}
		if ev.Progress != nil {
			t.Fatalf("event %d has progress %+v", i, ev.Progress)
		}
	}
}

func formatEvents(events []processing.ProgressEvent) string {
	var b strings.Builder
	for _, ev := range events {
		fmt.Fprintf(&b, "%s %s", ev.Stage, ev.Status)
		if ev.Progress != nil {
			fmt.Fprintf(&b, " %d/%d %s", ev.Progress.Current, ev.Progress.Total, ev.Progress.Unit)
		}
		if ev.Reason != "" {
			fmt.Fprintf(&b, " %q", ev.Reason)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatWant(want []wantEvent) string {
	var b strings.Builder
	for _, ev := range want {
		fmt.Fprintf(&b, "%s %s", ev.stage, ev.status)
		if ev.status == processing.ProgressAdvancing {
			fmt.Fprintf(&b, " %d/%d %s", ev.current, ev.total, ev.unit)
		}
		if ev.reason != "" {
			fmt.Fprintf(&b, " %q", ev.reason)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func progressOf(t *testing.T, f *fixture, jobID string) []processing.ProgressEvent {
	t.Helper()
	events, err := f.store.ProgressEvents(context.Background(), tenant, jobID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestProcessRecordsStageProgress(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english), textPage(2, english), processing.PageInfo{Number: 3})
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{pdfium}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if pdfium.callCount() != 2 {
		t.Fatalf("pdfium calls = %d, want 2", pdfium.callCount())
	}

	want := []wantEvent{
		step(processing.StageVerify, processing.ProgressStarted, ""),
		advance(processing.StageVerify, 3, 3, "pages", ""),
		step(processing.StageVerify, processing.ProgressCompleted, ""),
		step(processing.StageCacheLookup, processing.ProgressStarted, ""),
		advance(processing.StageCacheLookup, 1, 1, "lookups", ""),
		step(processing.StageCacheLookup, processing.ProgressCompleted, ""),
		step(processing.StagePrepare, processing.ProgressStarted, ""),
		advance(processing.StagePrepare, 3, 3, "targets", ""),
		step(processing.StagePrepare, processing.ProgressCompleted, ""),
		step(processing.StageExtract, processing.ProgressStarted, ""),
		step(processing.StageValidate, processing.ProgressStarted, ""),
	}
	for i, reason := range []string{"", "", "blank page"} {
		want = append(want,
			advance(processing.StageExtract, i+1, 3, "targets", reason),
			advance(processing.StageValidate, i+1, 3, "targets", reason),
		)
	}
	want = append(want,
		step(processing.StageExtract, processing.ProgressCompleted, ""),
		step(processing.StageValidate, processing.ProgressCompleted, ""),
		step(processing.StageAssemble, processing.ProgressStarted, ""),
		advance(processing.StageAssemble, 3, 3, "targets", ""),
		step(processing.StageAssemble, processing.ProgressCompleted, ""),
		step(processing.StagePublish, processing.ProgressStarted, ""),
		advance(processing.StagePublish, 3, 3, "pages", ""),
		step(processing.StagePublish, processing.ProgressCompleted, ""),
	)
	assertEvents(t, j.ID(), "worker-1", "", 0, progressOf(t, f, j.ID()), want)
}

func TestProcessSkipsStagesAfterCacheHit(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	p := f.pipeline(t)
	ctx := context.Background()
	first := f.queuedJob(t)
	if _, err := p.Process(ctx, tenant, first.ID(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	second := f.queuedJob(t)
	status, err := p.Process(ctx, tenant, second.ID(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}

	reason := "identical file already processed"
	code := document.ReasonHashMatch
	want := []wantEvent{
		step(processing.StageVerify, processing.ProgressStarted, ""),
		advance(processing.StageVerify, 1, 1, "pages", ""),
		step(processing.StageVerify, processing.ProgressCompleted, ""),
		step(processing.StageCacheLookup, processing.ProgressStarted, ""),
		advance(processing.StageCacheLookup, 1, 1, "lookups", ""),
		stepCode(processing.StageCacheLookup, processing.ProgressCompleted, reason, code),
		stepCode(processing.StagePrepare, processing.ProgressSkipped, reason, code),
		stepCode(processing.StageExtract, processing.ProgressSkipped, reason, code),
		stepCode(processing.StageValidate, processing.ProgressSkipped, reason, code),
		stepCode(processing.StageAssemble, processing.ProgressSkipped, reason, code),
		step(processing.StagePublish, processing.ProgressStarted, ""),
		advance(processing.StagePublish, 1, 1, "documents", ""),
		step(processing.StagePublish, processing.ProgressCompleted, reason),
	}
	assertEvents(t, second.ID(), "worker-1", "", 0, progressOf(t, f, second.ID()), want)
}

func TestProcessRecordsVerificationFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)
	delete(f.objects.data, storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}.String())

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusBlocked {
		t.Fatalf("status = %s, want blocked", status)
	}
	reason := "uploaded file is missing"
	want := []wantEvent{step(processing.StageVerify, processing.ProgressStarted, ""), step(processing.StageVerify, processing.ProgressFailed, reason)}
	for _, stage := range []processing.ProgressStage{
		processing.StageCacheLookup,
		processing.StagePrepare,
		processing.StageExtract,
		processing.StageValidate,
		processing.StageAssemble,
		processing.StagePublish,
	} {
		want = append(want, step(stage, processing.ProgressSkipped, reason))
	}
	assertEvents(t, j.ID(), "worker-1", "", 0, progressOf(t, f, j.ID()), want)
}

func TestProcessRecordsRetryableFailureBeforeReturn(t *testing.T) {
	t.Parallel()
	t.Run("verify", func(t *testing.T) {
		t.Parallel()
		f := newFixture(textPage(1, english))
		f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
		f.inspectErr = errors.New("inspector unavailable")
		j := f.queuedJob(t)

		_, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
		if err == nil || !strings.Contains(err.Error(), "inspector unavailable") {
			t.Fatalf("err = %v, want inspector failure", err)
		}
		events := progressOf(t, f, j.ID())
		assertEvents(t, j.ID(), "worker-1", "", 0, events, []wantEvent{
			step(processing.StageVerify, processing.ProgressStarted, ""),
			step(processing.StageVerify, processing.ProgressFailed, "inspect PDF: inspector unavailable"),
		})
	})

	t.Run("extract", func(t *testing.T) {
		t.Parallel()
		f := newFixture(textPage(1, english))
		ctx, cancel := context.WithCancel(context.Background())
		f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: func(document.Target) (string, error) {
			cancel()
			return "", errors.New("extractor stopped")
		}}}
		j := f.queuedJob(t)

		_, err := f.pipeline(t).Process(ctx, tenant, j.ID(), "worker-1")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context canceled", err)
		}
		events := progressOf(t, f, j.ID())
		var failed []processing.ProgressEvent
		for _, ev := range events {
			if ev.Status == processing.ProgressFailed {
				failed = append(failed, ev)
			}
			if (ev.Stage == processing.StageExtract || ev.Stage == processing.StageValidate) && ev.Status == processing.ProgressCompleted {
				t.Fatalf("stage %s completed after cancellation", ev.Stage)
			}
		}
		if len(failed) != 2 || failed[0].Stage != processing.StageExtract || failed[1].Stage != processing.StageValidate {
			t.Fatalf("failed stages = %s", formatEvents(failed))
		}
		if !strings.Contains(failed[0].Reason, context.Canceled.Error()) {
			t.Fatalf("failure reason = %q", failed[0].Reason)
		}
	})

	t.Run("publish", func(t *testing.T) {
		t.Parallel()
		f := newFixture(textPage(1, english))
		f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
		f.objects.failNext = errors.New("storage unavailable")
		j := f.queuedJob(t)
		p := f.pipeline(t)
		ctx := context.Background()

		_, err := p.Process(ctx, tenant, j.ID(), "worker-1")
		if err == nil || !strings.Contains(err.Error(), "storage unavailable") {
			t.Fatalf("err = %v, want storage failure", err)
		}
		events := progressOf(t, f, j.ID())
		last := events[len(events)-1]
		if last.Stage != processing.StagePublish || last.Status != processing.ProgressFailed {
			t.Fatalf("last event = %s %s, want publish failed", last.Stage, last.Status)
		}
		if !strings.Contains(last.Reason, "write content: storage unavailable") {
			t.Fatalf("reason = %q", last.Reason)
		}
		firstRun := last.RunID

		ctx = processing.WithRunIdentity(ctx, processing.RunIdentity{WorkflowID: "workflow-9", RunID: "run-9", Retry: 1})
		status, err := p.Process(ctx, tenant, j.ID(), "worker-1")
		if err != nil {
			t.Fatal(err)
		}
		if status != document.StatusReady {
			t.Fatalf("status = %s, want ready", status)
		}
		var resumed []processing.ProgressEvent
		for _, ev := range progressOf(t, f, j.ID()) {
			if ev.RunID == "run-9" {
				resumed = append(resumed, ev)
			}
		}
		if len(resumed) == 0 || resumed[len(resumed)-1].Status != processing.ProgressCompleted || resumed[len(resumed)-1].Stage != processing.StagePublish {
			t.Fatalf("resumed tail =\n%s", formatEvents(resumed))
		}
		if resumed[0].RunID == firstRun || resumed[0].Retry != 1 || resumed[0].WorkflowID != "workflow-9" {
			t.Fatalf("resumed identity = %s retry %d run %s", resumed[0].WorkflowID, resumed[0].Retry, resumed[0].RunID)
		}
	})
}

func TestProcessSkipsPublishWhenBlocked(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english), processing.PageInfo{Number: 2, Images: 1}, processing.PageInfo{Number: 3, Images: 1})
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	f.chains[document.ContentScan] = []processing.Extractor{&extractor{name: "docling", respond: func(document.Target) (string, error) {
		return "", errors.New("no text")
	}}}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusBlocked {
		t.Fatalf("status = %s, want blocked", status)
	}
	reason := "only 1 of 3 pages passed validation"
	events := progressOf(t, f, j.ID())
	if len(events) < 2 {
		t.Fatal("expected assemble and skipped publish")
	}
	assemble := events[len(events)-2]
	publish := events[len(events)-1]
	if assemble.Stage != processing.StageAssemble || assemble.Status != processing.ProgressCompleted || assemble.Reason != reason {
		t.Fatalf("assemble = %s %s %q", assemble.Stage, assemble.Status, assemble.Reason)
	}
	if publish.Stage != processing.StagePublish || publish.Status != processing.ProgressSkipped || publish.Reason != reason {
		t.Fatalf("publish = %s %s %q", publish.Stage, publish.Status, publish.Reason)
	}
}

func TestProcessRunIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)
	ctx := processing.WithRunIdentity(context.Background(), processing.RunIdentity{
		WorkflowID: "workflow-7",
		RunID:      "run-7",
		Retry:      2,
	})

	if _, err := f.pipeline(t).Process(ctx, tenant, j.ID(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	events := progressOf(t, f, j.ID())
	if len(events) == 0 {
		t.Fatal("no progress events")
	}
	assertEvents(t, j.ID(), "workflow-7", "run-7", 2, events, happyPage(1))
}

func TestProcessRejectsNegativeRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)
	ctx := processing.WithRunIdentity(context.Background(), processing.RunIdentity{Retry: -1})

	_, err := f.pipeline(t).Process(ctx, tenant, j.ID(), "worker-1")
	if !errors.Is(err, processing.ErrInvalidProgressEvent) {
		t.Fatalf("err = %v, want invalid progress event", err)
	}
	if events := progressOf(t, f, j.ID()); len(events) != 0 {
		t.Fatalf("events = %s", formatEvents(events))
	}
	if got := f.job(t, j.ID()); got.Status() != document.StatusQueued {
		t.Fatalf("status = %s, want queued", got.Status())
	}
}

func TestProcessWithoutProgressSink(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.recordProgress = false
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if events := progressOf(t, f, j.ID()); len(events) != 0 {
		t.Fatalf("events = %s", formatEvents(events))
	}
}

func TestProcessClaimFailureRecordsNoEvents(t *testing.T) {
	t.Parallel()
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)
	if err := j.Claim("other-worker", time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "worker-1")
	if !errors.Is(err, processing.ErrClaimed) {
		t.Fatalf("err = %v, want claimed", err)
	}
	if events := progressOf(t, f, j.ID()); len(events) != 0 {
		t.Fatalf("events = %s", formatEvents(events))
	}
}

func happyPage(pages int) []wantEvent {
	return []wantEvent{
		step(processing.StageVerify, processing.ProgressStarted, ""),
		advance(processing.StageVerify, pages, pages, "pages", ""),
		step(processing.StageVerify, processing.ProgressCompleted, ""),
		step(processing.StageCacheLookup, processing.ProgressStarted, ""),
		advance(processing.StageCacheLookup, 1, 1, "lookups", ""),
		step(processing.StageCacheLookup, processing.ProgressCompleted, ""),
		step(processing.StagePrepare, processing.ProgressStarted, ""),
		advance(processing.StagePrepare, pages, pages, "targets", ""),
		step(processing.StagePrepare, processing.ProgressCompleted, ""),
		step(processing.StageExtract, processing.ProgressStarted, ""),
		step(processing.StageValidate, processing.ProgressStarted, ""),
		advance(processing.StageExtract, 1, pages, "targets", ""),
		advance(processing.StageValidate, 1, pages, "targets", ""),
		step(processing.StageExtract, processing.ProgressCompleted, ""),
		step(processing.StageValidate, processing.ProgressCompleted, ""),
		step(processing.StageAssemble, processing.ProgressStarted, ""),
		advance(processing.StageAssemble, pages, pages, "targets", ""),
		step(processing.StageAssemble, processing.ProgressCompleted, ""),
		step(processing.StagePublish, processing.ProgressStarted, ""),
		advance(processing.StagePublish, pages, pages, "pages", ""),
		step(processing.StagePublish, processing.ProgressCompleted, ""),
	}
}
