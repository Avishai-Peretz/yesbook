package processing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/memory"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
	"github.com/avi-p/yesbook/internal/storage"
)

const tenant = "tenant-a"

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type objects struct {
	mu       sync.Mutex
	data     map[string][]byte
	failNext error
}

func newObjects() *objects { return &objects{data: make(map[string][]byte)} }

func (o *objects) Read(_ context.Context, ref storage.Ref, maxBytes int64) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.data[ref.String()]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if int64(len(b)) > maxBytes {
		return nil, storage.ErrTooLarge
	}
	return b, nil
}

func (o *objects) Write(_ context.Context, ref storage.Ref, _ string, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if ref.Bucket == storage.BucketDerived && o.failNext != nil {
		err := o.failNext
		o.failNext = nil
		return err
	}
	o.data[ref.String()] = data
	return nil
}

type inspector struct{ info processing.PDFInfo }

func (i inspector) Inspect(context.Context, []byte) (processing.PDFInfo, error) { return i.info, nil }

type failingInspector struct{ err error }

func (f failingInspector) Inspect(context.Context, []byte) (processing.PDFInfo, error) {
	return processing.PDFInfo{}, f.err
}

// extractor returns one paragraph with the text produced by respond; an
// error from respond simulates an extractor failure.
type extractor struct {
	name    string
	respond func(t document.Target) (string, error)
	mu      sync.Mutex
	calls   []document.Target
}

func (e *extractor) Name() string    { return e.name }
func (e *extractor) Version() string { return "1" }

func (e *extractor) Extract(_ context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	e.mu.Lock()
	e.calls = append(e.calls, req.Target)
	e.mu.Unlock()
	text, err := e.respond(req.Target)
	if err != nil {
		return document.Extraction{}, err
	}
	return document.Extraction{
		Blocks: []document.Block{{
			Kind: document.BlockParagraph,
			Text: text,
			BBox: &document.BBox{Left: 72, Top: 720, Right: 400, Bottom: 700},
		}},
		CostMicros: 1000,
	}, nil
}

func (e *extractor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func echoTextLayer(t document.Target) (string, error) { return t.TextLayer, nil }

// blocksExtractor returns arbitrary blocks so tests can exercise figure escalation.
type blocksExtractor struct {
	name    string
	respond func(t document.Target) ([]document.Block, error)
	mu      sync.Mutex
	calls   []document.Target
}

func (e *blocksExtractor) Name() string    { return e.name }
func (e *blocksExtractor) Version() string { return "1" }

func (e *blocksExtractor) Extract(_ context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	e.mu.Lock()
	e.calls = append(e.calls, req.Target)
	e.mu.Unlock()
	blocks, err := e.respond(req.Target)
	if err != nil {
		return document.Extraction{}, err
	}
	return document.Extraction{Blocks: blocks, CostMicros: 1000}, nil
}

func (e *blocksExtractor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

type fixture struct {
	store          *memory.Store
	objects        *objects
	pdf            []byte
	info           processing.PDFInfo
	chains         map[document.ContentKind][]processing.Extractor
	cfg            processing.Config
	ids            int
	recordProgress bool
	inspectErr     error
}

func newFixture(pages ...processing.PageInfo) *fixture {
	return &fixture{
		store:          memory.New(),
		objects:        newObjects(),
		pdf:            []byte("%PDF-1.7 test document"),
		info:           processing.PDFInfo{Pages: pages},
		chains:         map[document.ContentKind][]processing.Extractor{},
		cfg:            processing.DefaultConfig(),
		recordProgress: true,
	}
}

func (f *fixture) newID() string {
	f.ids++
	return fmt.Sprintf("id-%03d", f.ids)
}

func (f *fixture) pipeline(t *testing.T) *processing.Pipeline {
	t.Helper()
	var ins processing.Inspector = inspector{info: f.info}
	if f.inspectErr != nil {
		ins = failingInspector{err: f.inspectErr}
	}
	deps := processing.Dependencies{
		Jobs:      f.store,
		Versions:  f.store,
		Attempts:  f.store,
		Objects:   f.objects,
		Inspector: ins,
		Router:    processing.NewRouter(f.chains),
		Now:       func() time.Time { return t0 },
		NewID:     f.newID,
	}
	if f.recordProgress {
		deps.Progress = f.store
	}
	p, err := processing.NewPipeline(deps, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// queuedJob registers a job whose upload is already in staging.
func (f *fixture) queuedJob(t *testing.T) *document.Job {
	t.Helper()
	ctx := context.Background()
	j, err := document.NewJob(f.newID(), document.NewJobParams{
		TenantID: tenant, FileName: "lesson.pdf", ContentType: document.ContentTypePDF, SizeBytes: int64(len(f.pdf)),
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateJob(ctx, j, j.Event(f.newID(), t0)); err != nil {
		t.Fatal(err)
	}
	if err := j.Queue(t0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	ref := storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}
	if err := f.objects.Write(ctx, ref, document.ContentTypePDF, f.pdf); err != nil {
		t.Fatal(err)
	}
	return j
}

func (f *fixture) job(t *testing.T, id string) *document.Job {
	t.Helper()
	j, err := f.store.GetJob(context.Background(), tenant, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (f *fixture) content(t *testing.T, versionID string) document.Content {
	t.Helper()
	v, err := f.store.GetVersion(context.Background(), tenant, versionID)
	if err != nil {
		t.Fatal(err)
	}
	var c document.Content
	raw := f.objects.data[storage.Ref{Bucket: storage.BucketDerived, Name: v.ContentObject}.String()]
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func textPage(n int, text string) processing.PageInfo {
	return processing.PageInfo{Number: n, Text: text}
}

const english = "The quick brown fox jumps over the lazy dog near the river bank"

func TestProcessTextDocumentIsReady(t *testing.T) {
	f := newFixture(textPage(1, english), textPage(2, english+" again"), processing.PageInfo{Number: 3})
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
	got := f.job(t, j.ID())
	if got.VersionID() == "" || got.FileHash() == "" {
		t.Fatalf("job = %+v, want version and hash", got.Record())
	}
	c := f.content(t, got.VersionID())
	if len(c.Blocks) != 2 || c.Pages != 3 || c.Coverage != 1 {
		t.Fatalf("content = %d blocks, %d pages, coverage %v", len(c.Blocks), c.Pages, c.Coverage)
	}
	for _, b := range c.Blocks {
		if b.Provenance.Extractor != "pdfium" || b.Provenance.AttemptID == "" || b.Page == 0 {
			t.Errorf("block missing provenance: %+v", b)
		}
	}
	if q := f.store.Quality(got.VersionID()); len(q) != 3 {
		t.Errorf("quality results = %d, want 3", len(q))
	}
	if pdfium.callCount() != 2 {
		t.Errorf("pdfium calls = %d, want 2 (blank page skipped)", pdfium.callCount())
	}
	original := storage.Ref{Bucket: storage.BucketOriginals, Name: got.OriginalObject()}
	if _, ok := f.objects.data[original.String()]; !ok {
		t.Error("verified original was not stored")
	}
}

func TestProcessHashMatchReturnsExistingVersion(t *testing.T) {
	f := newFixture(textPage(1, english))
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{pdfium}
	p := f.pipeline(t)
	ctx := context.Background()

	first := f.queuedJob(t)
	if _, err := p.Process(ctx, tenant, first.ID(), "w"); err != nil {
		t.Fatal(err)
	}
	second := f.queuedJob(t)
	status, err := p.Process(ctx, tenant, second.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if a, b := f.job(t, first.ID()).VersionID(), f.job(t, second.ID()).VersionID(); a != b {
		t.Errorf("version IDs differ: %s vs %s", a, b)
	}
	if pdfium.callCount() != 1 {
		t.Errorf("pdfium calls = %d, want 1", pdfium.callCount())
	}
}

func TestProcessEscalatesToNextExtractor(t *testing.T) {
	f := newFixture(textPage(1, english))
	garbled := &extractor{name: "pdfium", respond: func(document.Target) (string, error) {
		return "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod", nil
	}}
	docling := &extractor{name: "docling", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{garbled, docling}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if docling.callCount() != 1 {
		t.Fatalf("docling calls = %d, want 1", docling.callCount())
	}
	c := f.content(t, f.job(t, j.ID()).VersionID())
	if c.Blocks[0].Provenance.Extractor != "docling" {
		t.Errorf("block extractor = %s, want docling", c.Blocks[0].Provenance.Extractor)
	}
	if n := len(f.store.Attempts()); n != 2 {
		t.Errorf("attempts = %d, want 2", n)
	}
}

func TestProcessEscalatesWhenEmbeddedImagesLackFigures(t *testing.T) {
	f := newFixture(processing.PageInfo{Number: 1, Text: english, Images: 1})
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	docling := &extractor{name: "docling", respond: echoTextLayer}
	gemini := &blocksExtractor{name: "gemini", respond: func(t document.Target) ([]document.Block, error) {
		return []document.Block{
			{Kind: document.BlockParagraph, Text: t.TextLayer},
			{Kind: document.BlockFigure, Text: "Portrait photo"},
		}, nil
	}}
	f.chains[document.ContentText] = []processing.Extractor{pdfium, docling, gemini}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if pdfium.callCount() != 1 || docling.callCount() != 1 || gemini.callCount() != 1 {
		t.Fatalf("calls pdfium=%d docling=%d gemini=%d, want 1 each", pdfium.callCount(), docling.callCount(), gemini.callCount())
	}
	c := f.content(t, f.job(t, j.ID()).VersionID())
	foundFigure := false
	for _, b := range c.Blocks {
		if b.Kind == document.BlockFigure {
			foundFigure = true
			if b.Provenance.Extractor != "gemini" {
				t.Errorf("figure extractor = %s, want gemini", b.Provenance.Extractor)
			}
		}
	}
	if !foundFigure {
		t.Fatalf("blocks = %+v, want a figure from gemini", c.Blocks)
	}
	if n := len(f.store.Attempts()); n != 3 {
		t.Fatalf("attempts = %d, want 3", n)
	}
}

// CV scenario: Automatic+images, Docling unset (not in chain), Gemini configured.
// PDFium must decline figures_missing and escalate to Gemini — not jump to empty review.
func TestProcessEscalatesPastUnsetDoclingToGemini(t *testing.T) {
	f := newFixture(processing.PageInfo{Number: 1, Text: english, Images: 1})
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	gemini := &blocksExtractor{name: "gemini", respond: func(t document.Target) ([]document.Block, error) {
		return []document.Block{
			{Kind: document.BlockParagraph, Text: t.TextLayer, BBox: &document.BBox{Left: 1, Top: 10, Right: 2, Bottom: 9}},
			{Kind: document.BlockFigure, Text: "CV portrait"},
		}, nil
	}}
	f.chains[document.ContentText] = []processing.Extractor{pdfium, gemini}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s reason %q, want ready", status, f.job(t, j.ID()).Reason())
	}
	if pdfium.callCount() != 1 {
		t.Fatalf("pdfium calls = %d, want 1", pdfium.callCount())
	}
	if gemini.callCount() != 1 {
		t.Fatalf("gemini calls = %d, want 1 (must escalate when Docling is unset)", gemini.callCount())
	}
	attempts := f.store.Attempts()
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (pdfium declined + gemini chosen)", len(attempts))
	}
	if attempts[0].ReasonCode != document.ReasonFiguresMissing {
		t.Errorf("pdfium reason = %s, want figures_missing", attempts[0].ReasonCode)
	}
	if attempts[1].Extractor != "gemini" || attempts[1].Decision != document.DecisionChosen {
		t.Errorf("second attempt = %+v, want chosen gemini", attempts[1])
	}
	events, err := f.store.ProgressEvents(context.Background(), tenant, j.ID())
	if err != nil {
		t.Fatal(err)
	}
	foundUnset := false
	for _, ev := range events {
		if ev.ReasonCode == document.ReasonExtractorUnset {
			foundUnset = true
			break
		}
	}
	if !foundUnset {
		t.Fatal("expected extractor_unset progress when Docling is not configured")
	}
}

func TestProcessNeedsReviewKeepsBlocksWhenChainExhausted(t *testing.T) {
	f := newFixture(processing.PageInfo{Number: 1, Text: english, Images: 1})
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{pdfium}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", status)
	}
	c := f.content(t, f.job(t, j.ID()).VersionID())
	if len(c.Blocks) == 0 {
		t.Fatal("needs_review content must keep candidate blocks for review")
	}
	if len(c.Review) != 1 {
		t.Fatalf("review items = %d, want 1", len(c.Review))
	}
	if c.Review[0].Code != document.ReasonFiguresMissing {
		t.Errorf("review code = %q, want figures_missing", c.Review[0].Code)
	}
	if !strings.Contains(strings.Join(c.Review[0].FailedChecks, ","), "figures") {
		t.Errorf("failed_checks = %v, want figures", c.Review[0].FailedChecks)
	}
	events, err := f.store.ProgressEvents(context.Background(), tenant, j.ID())
	if err != nil {
		t.Fatal(err)
	}
	foundExhausted := false
	for _, ev := range events {
		if ev.ReasonCode == document.ReasonChainExhausted {
			foundExhausted = true
			if !strings.Contains(ev.ReasonMessage, "no further extractors") {
				t.Errorf("chain_exhausted message = %q", ev.ReasonMessage)
			}
		}
	}
	if !foundExhausted {
		t.Fatal("expected chain_exhausted when only pdfium is configured")
	}
}

func TestProcessRetryAddsNeighbourContext(t *testing.T) {
	// Short text, no images → ContentScan without triggering the figures check.
	f := newFixture(textPage(1, english), processing.PageInfo{Number: 2, Text: "scan"})
	failing := &extractor{name: "docling", respond: func(document.Target) (string, error) {
		return "", errors.New("service unavailable")
	}}
	gemini := &extractor{name: "gemini", respond: func(document.Target) (string, error) { return "handwritten notes", nil }}
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	f.chains[document.ContentScan] = []processing.Extractor{failing, gemini}
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if ctx := gemini.calls[0].Context; !strings.Contains(ctx, "quick brown fox") {
		t.Errorf("retry context = %q, want neighbouring page text", ctx)
	}
	if failing.calls[0].Context != "" {
		t.Error("first attempt should not carry context")
	}
}

func TestProcessOutcomes(t *testing.T) {
	failing := func(document.Target) (string, error) { return "", errors.New("boom") }
	tests := []struct {
		name    string
		pages   []processing.PageInfo
		scan    func(document.Target) (string, error)
		want    document.Status
		review  int
		missing int
	}{
		{
			name:    "partial when one of two pages fails",
			pages:   []processing.PageInfo{textPage(1, english), {Number: 2, Images: 1}},
			scan:    failing,
			want:    document.StatusPartial,
			missing: 1,
		},
		{
			name:  "blocked when most pages fail",
			pages: []processing.PageInfo{textPage(1, english), {Number: 2, Images: 1}, {Number: 3, Images: 1}},
			scan:  failing,
			want:  document.StatusBlocked,
		},
		{
			name:  "needs review when text is illegible",
			pages: []processing.PageInfo{textPage(1, english), {Number: 2, Images: 1}},
			scan: func(document.Target) (string, error) {
				return "\uFFFD\uFFFD\uFFFD text", nil
			},
			want:   document.StatusNeedsReview,
			review: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(tt.pages...)
			f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
			f.chains[document.ContentScan] = []processing.Extractor{&extractor{name: "docling", respond: tt.scan}}
			j := f.queuedJob(t)

			status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
			if err != nil {
				t.Fatal(err)
			}
			if status != tt.want {
				t.Fatalf("status = %s, want %s (reason %q)", status, tt.want, f.job(t, j.ID()).Reason())
			}
			if !status.Published() {
				return
			}
			c := f.content(t, f.job(t, j.ID()).VersionID())
			if len(c.Review) != tt.review || len(c.Missing) != tt.missing {
				t.Errorf("review = %d, missing = %d, want %d and %d", len(c.Review), len(c.Missing), tt.review, tt.missing)
			}
		})
	}
}

func TestProcessBlocksInvalidFiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f *fixture, j *document.Job)
		reason string
	}{
		{
			name: "missing upload",
			mutate: func(f *fixture, j *document.Job) {
				delete(f.objects.data, storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}.String())
			},
			reason: "missing",
		},
		{
			name: "not a pdf",
			mutate: func(f *fixture, j *document.Job) {
				f.objects.data[storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}.String()] = []byte("hello, this is not pdf")
			},
			reason: "not a PDF",
		},
		{
			name: "size mismatch",
			mutate: func(f *fixture, j *document.Job) {
				f.objects.data[storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}.String()] = []byte("%PDF-1.7")
			},
			reason: "declared",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(textPage(1, english))
			f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
			j := f.queuedJob(t)
			tt.mutate(f, j)

			status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
			if err != nil {
				t.Fatal(err)
			}
			got := f.job(t, j.ID())
			if status != document.StatusBlocked || !strings.Contains(got.Reason(), tt.reason) {
				t.Errorf("status = %s, reason = %q, want blocked containing %q", status, got.Reason(), tt.reason)
			}
		})
	}
}

func TestProcessStopsAtBudget(t *testing.T) {
	f := newFixture(textPage(1, english), textPage(2, english), textPage(3, english))
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{pdfium}
	f.cfg.Budget.MaxAttempts = 2
	f.cfg.MinPartialCoverage = 0.5
	j := f.queuedJob(t)

	status, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusPartial {
		t.Fatalf("status = %s, want partial", status)
	}
	if pdfium.callCount() != 2 {
		t.Errorf("pdfium calls = %d, want 2", pdfium.callCount())
	}
	c := f.content(t, f.job(t, j.ID()).VersionID())
	if len(c.Missing) != 1 || !strings.Contains(c.Missing[0].Reason, "attempt") {
		t.Errorf("missing = %+v, want one page stopped by the attempt cap", c.Missing)
	}
}

func TestProcessResumesFromEvidence(t *testing.T) {
	f := newFixture(textPage(1, english), textPage(2, english))
	pdfium := &extractor{name: "pdfium", respond: echoTextLayer}
	f.chains[document.ContentText] = []processing.Extractor{pdfium}
	f.objects.failNext = errors.New("storage unavailable")
	j := f.queuedJob(t)
	p := f.pipeline(t)
	ctx := context.Background()

	if _, err := p.Process(ctx, tenant, j.ID(), "w"); err == nil {
		t.Fatal("first run should fail on the content write")
	}
	status, err := p.Process(ctx, tenant, j.ID(), "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != document.StatusReady {
		t.Fatalf("status = %s, want ready", status)
	}
	if pdfium.callCount() != 2 {
		t.Errorf("pdfium calls = %d, want 2 (second run reuses evidence)", pdfium.callCount())
	}
}

func TestProcessRejectsConcurrentClaim(t *testing.T) {
	f := newFixture(textPage(1, english))
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	j := f.queuedJob(t)
	if err := j.Claim("other-worker", time.Hour, t0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	_, err := f.pipeline(t).Process(context.Background(), tenant, j.ID(), "w")
	if !errors.Is(err, processing.ErrClaimed) {
		t.Errorf("err = %v, want ErrClaimed", err)
	}
}

func TestBlockIsIdempotent(t *testing.T) {
	f := newFixture(textPage(1, english))
	j := f.queuedJob(t)
	p := f.pipeline(t)
	ctx := context.Background()

	for range 2 {
		if err := p.Block(ctx, tenant, j.ID(), "workflow deadline exceeded"); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.job(t, j.ID()); got.Status() != document.StatusBlocked {
		t.Errorf("status = %s, want blocked", got.Status())
	}
}

func TestPipelineVersionPinsExtractors(t *testing.T) {
	f := newFixture()
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "pdfium", respond: echoTextLayer}}
	a := f.pipeline(t).Version()
	f.chains[document.ContentText] = []processing.Extractor{&extractor{name: "docling", respond: echoTextLayer}}
	if b := f.pipeline(t).Version(); a == b {
		t.Errorf("version %s did not change with the extractor chain", a)
	}
}
