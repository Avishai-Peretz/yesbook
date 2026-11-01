package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

// policyVersion changes whenever routing or assembly rules change in a way
// that should invalidate cached versions.
const policyVersion = "2026-11-01"

const (
	minTextLayerChars = 20
	maxContextRunes   = 2000
	maxErrorRunes     = 500
)

type Dependencies struct {
	Jobs      JobRepository
	Versions  VersionRepository
	Attempts  AttemptLog
	Objects   ObjectStore
	Inspector Inspector
	Router    *Router
	Logger    *slog.Logger
	Now       func() time.Time
	NewID     func() string
}

type Config struct {
	Budget             document.Budget
	Lease              time.Duration
	AttemptTimeout     time.Duration
	MaxPages           int
	MinPartialCoverage float64
	Validator          Validator
}

func DefaultConfig() Config {
	return Config{
		Budget: document.Budget{
			MaxAttempts:   200,
			MaxDuration:   30 * time.Minute,
			MaxCostMicros: 2_000_000,
		},
		Lease:              15 * time.Minute,
		AttemptTimeout:     2 * time.Minute,
		MaxPages:           500,
		MinPartialCoverage: 0.5,
		Validator:          DefaultValidator(),
	}
}

type Pipeline struct {
	jobs      JobRepository
	versions  VersionRepository
	attempts  AttemptLog
	objects   ObjectStore
	inspector Inspector
	router    *Router
	log       *slog.Logger
	now       func() time.Time
	newID     func() string
	cfg       Config
	evidence  evidenceStore
	version   string
}

func NewPipeline(d Dependencies, cfg Config) (*Pipeline, error) {
	if d.Jobs == nil || d.Versions == nil || d.Attempts == nil || d.Objects == nil || d.Inspector == nil || d.Router == nil {
		return nil, errors.New("processing: missing dependency")
	}
	p := &Pipeline{
		jobs:      d.Jobs,
		versions:  d.Versions,
		attempts:  d.Attempts,
		objects:   d.Objects,
		inspector: d.Inspector,
		router:    d.Router,
		log:       d.Logger,
		now:       d.Now,
		newID:     d.NewID,
		cfg:       cfg,
		evidence:  evidenceStore{objects: d.Objects},
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.newID == nil {
		p.newID = document.NewID
	}
	p.version = pipelineVersion(policyVersion, d.Router.Signature(), cfg.Validator.String(), fmt.Sprint(cfg.MinPartialCoverage))
	return p, nil
}

// Version identifies the pipeline configuration, including pinned extractor versions.
func (p *Pipeline) Version() string { return p.version }

// Process runs the pipeline for one job and returns its final status.
// Content problems end in a blocked job and a nil error; a returned error
// means the run should be retried, and the retry resumes from saved evidence.
func (p *Pipeline) Process(ctx context.Context, tenantID, jobID, worker string) (document.Status, error) {
	log := p.log.With("tenant_id", tenantID, "job_id", jobID, "worker", worker)

	j, err := p.jobs.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return "", fmt.Errorf("load job: %w", err)
	}
	if j.Status().Final() {
		log.InfoContext(ctx, "job already finished", "status", j.Status())
		return j.Status(), nil
	}
	if err := j.Claim(worker, p.cfg.Lease, p.now().UTC()); err != nil {
		return "", fmt.Errorf("%w: %w", ErrClaimed, err)
	}
	if err := p.jobs.UpdateJob(ctx, j); err != nil {
		if errors.Is(err, document.ErrConflict) {
			return "", fmt.Errorf("%w: %w", ErrClaimed, err)
		}
		return "", fmt.Errorf("claim job: %w", err)
	}
	log.InfoContext(ctx, "processing started")

	pdf, info, reason, err := p.verify(ctx, j)
	if err != nil {
		return "", err
	}
	if reason != "" {
		return p.finish(ctx, log, j, document.StatusBlocked, "", reason)
	}

	cacheKey := document.CacheKey(j.TenantID(), j.FileHash(), p.version)
	cached, ok, err := p.versions.FindCachedVersion(ctx, cacheKey)
	if err != nil {
		return "", fmt.Errorf("look up cached version: %w", err)
	}
	if ok {
		log.InfoContext(ctx, "hash match, returning existing version", "version_id", cached.ID)
		return p.finish(ctx, log, j, document.StatusReady, cached.ID, "identical file already processed")
	}

	targets, err := p.prepare(ctx, j, info)
	if err != nil {
		return "", err
	}
	results, err := p.extractAll(ctx, log, j, pdf, targets)
	if err != nil {
		return "", err
	}
	a := assemble(results, p.cfg.MinPartialCoverage)
	if a.outcome == document.StatusBlocked {
		return p.finish(ctx, log, j, document.StatusBlocked, "", a.reason)
	}
	return p.commit(ctx, log, j, a, len(targets), cacheKey)
}

// Block marks a job blocked after the workflow gave up on it.
func (p *Pipeline) Block(ctx context.Context, tenantID, jobID, reason string) error {
	j, err := p.jobs.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}
	if j.Status().Final() {
		return nil
	}
	now := p.now().UTC()
	if err := j.Block(reason, now); err != nil {
		return err
	}
	if err := p.jobs.UpdateJob(ctx, j, j.Event(p.newID(), now)); err != nil {
		return fmt.Errorf("block job: %w", err)
	}
	return nil
}

func (p *Pipeline) finish(ctx context.Context, log *slog.Logger, j *document.Job, outcome document.Status, versionID, reason string) (document.Status, error) {
	now := p.now().UTC()
	if err := j.Complete(outcome, versionID, reason, now); err != nil {
		return "", err
	}
	if err := p.jobs.UpdateJob(ctx, j, j.Event(p.newID(), now)); err != nil {
		return "", fmt.Errorf("complete job: %w", err)
	}
	log.InfoContext(ctx, "processing finished", "status", outcome, "reason", reason)
	return outcome, nil
}

type verification struct {
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
	Pages     int    `json:"pages"`
}

// verify checks file type, size and PDF structure, computes the file hash
// and stores the verified original. A non-empty reason means the file is
// unusable and the job must be blocked.
func (p *Pipeline) verify(ctx context.Context, j *document.Job) ([]byte, PDFInfo, string, error) {
	pdf, err := p.objects.Read(ctx, storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}, document.MaxSizeBytes)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return nil, PDFInfo{}, "uploaded file is missing", nil
	case errors.Is(err, storage.ErrTooLarge):
		return nil, PDFInfo{}, "file exceeds the size limit", nil
	case err != nil:
		return nil, PDFInfo{}, "", fmt.Errorf("read upload: %w", err)
	}
	if int64(len(pdf)) != j.SizeBytes() {
		return nil, PDFInfo{}, fmt.Sprintf("file is %d bytes, %d were declared", len(pdf), j.SizeBytes()), nil
	}
	if !bytes.Contains(pdf[:min(len(pdf), 1024)], []byte("%PDF-")) {
		return nil, PDFInfo{}, "file is not a PDF", nil
	}

	info, err := p.inspector.Inspect(ctx, pdf)
	switch {
	case errors.Is(err, ErrEncryptedPDF):
		return nil, PDFInfo{}, "PDF is password protected", nil
	case errors.Is(err, ErrUnreadablePDF):
		return nil, PDFInfo{}, "PDF structure is invalid", nil
	case err != nil:
		return nil, PDFInfo{}, "", fmt.Errorf("inspect PDF: %w", err)
	}
	if len(info.Pages) == 0 {
		return nil, PDFInfo{}, "PDF has no pages", nil
	}
	if len(info.Pages) > p.cfg.MaxPages {
		return nil, PDFInfo{}, fmt.Sprintf("PDF has %d pages, the limit is %d", len(info.Pages), p.cfg.MaxPages), nil
	}

	sum := sha256.Sum256(pdf)
	if err := j.RecordHash(hex.EncodeToString(sum[:]), p.now().UTC()); err != nil {
		return nil, PDFInfo{}, "", err
	}
	original := storage.Ref{Bucket: storage.BucketOriginals, Name: j.OriginalObject()}
	if err := p.objects.Write(ctx, original, document.ContentTypePDF, pdf); err != nil {
		return nil, PDFInfo{}, "", fmt.Errorf("store verified original: %w", err)
	}
	v := verification{Hash: j.FileHash(), SizeBytes: j.SizeBytes(), Pages: len(info.Pages)}
	if err := p.evidence.save(ctx, evidenceRef(j, "verify"), v); err != nil {
		return nil, PDFInfo{}, "", err
	}
	return pdf, info, "", nil
}

// prepare splits the PDF into page targets with their text layer and
// language, reusing saved targets on a retried run.
func (p *Pipeline) prepare(ctx context.Context, j *document.Job, info PDFInfo) ([]document.Target, error) {
	ref := evidenceRef(j, "prepare")
	var targets []document.Target
	ok, err := p.evidence.load(ctx, ref, &targets)
	if err != nil || ok {
		return targets, err
	}
	targets = buildTargets(info)
	if err := p.evidence.save(ctx, ref, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func buildTargets(info PDFInfo) []document.Target {
	targets := make([]document.Target, 0, len(info.Pages))
	for _, pg := range info.Pages {
		chars := countChars(pg.Text)
		content := document.ContentScan
		switch {
		case chars >= minTextLayerChars:
			content = document.ContentText
		case chars == 0 && pg.Images == 0:
			content = document.ContentBlank
		}
		t := document.Target{
			ID:       fmt.Sprintf("p%04d", pg.Number),
			Kind:     document.TargetPage,
			Pages:    []int{pg.Number},
			Content:  content,
			Language: detectLanguage(pg.Text),
		}
		if content == document.ContentText {
			t.TextLayer = pg.Text
			t.TextLayerChars = chars
		}
		targets = append(targets, t)
	}
	return targets
}

func (p *Pipeline) extractAll(ctx context.Context, log *slog.Logger, j *document.Job, pdf []byte, targets []document.Target) ([]targetResult, error) {
	spend, err := p.attempts.Spend(ctx, j.ID())
	if err != nil {
		return nil, fmt.Errorf("load spend: %w", err)
	}
	results := make([]targetResult, 0, len(targets))
	for i := range targets {
		r, err := p.extractTarget(ctx, log, j, pdf, targets, i, &spend)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	log.InfoContext(ctx, "extraction finished", "targets", len(targets), "attempts", spend.Attempts, "cost_micros", spend.CostMicros)
	return results, nil
}

// extractTarget routes one target through its extractor chain until an
// extraction passes validation, the chain runs out, or the budget is spent.
// Retries escalate to the next extractor and add neighbouring-page context.
func (p *Pipeline) extractTarget(ctx context.Context, log *slog.Logger, j *document.Job, pdf []byte, targets []document.Target, i int, spend *document.Spend) (targetResult, error) {
	t := targets[i]
	if t.Content == document.ContentBlank {
		q := document.QualityResult{TargetID: t.ID, Verdict: document.VerdictAccept, Reason: "blank page"}
		return targetResult{target: t, state: targetAccepted, extraction: &document.Extraction{TargetID: t.ID}, quality: &q}, nil
	}

	res := targetResult{target: t, state: targetFailed, reason: fmt.Sprintf("no extractor available for %s content", t.Content)}
	var tried []string
	for attempt := 1; ; attempt++ {
		e, ok := p.router.Next(t, tried)
		if !ok {
			return res, nil
		}
		tried = append(tried, e.Name())
		if attempt > 1 {
			t.Context = neighborContext(targets, i)
		}

		var ex document.Extraction
		found, err := p.evidence.load(ctx, evidenceRef(j, "extract", extractionKey(t, e)), &ex)
		if err != nil {
			return res, err
		}
		if !found {
			if reason, over := p.cfg.Budget.Exceeded(*spend, p.now()); over {
				log.WarnContext(ctx, "budget exhausted", "target_id", t.ID, "reason", reason)
				if res.state == targetFailed {
					res.reason = reason
				}
				return res, nil
			}
			var succeeded bool
			ex, succeeded, err = p.runExtractor(ctx, log, j, t, e, pdf, attempt, spend)
			if err != nil {
				return res, err
			}
			if !succeeded {
				if res.state == targetFailed {
					res.reason = fmt.Sprintf("extractor %s failed", e.Name())
				}
				continue
			}
		}

		q := p.cfg.Validator.Validate(t, ex, p.router.HasNext(t, tried))
		if err := p.evidence.save(ctx, evidenceRef(j, "validate", extractionKey(t, e)), q); err != nil {
			return res, err
		}
		log.InfoContext(ctx, "target validated", "target_id", t.ID, "extractor", e.Name(), "verdict", q.Verdict, "reason", q.Reason)

		cand := targetResult{target: t, extraction: &ex, quality: &q, reason: q.Reason}
		switch q.Verdict {
		case document.VerdictAccept:
			cand.state = targetAccepted
			return cand, nil
		case document.VerdictReview:
			cand.state = targetReview
			return cand, nil
		case document.VerdictReject:
			if res.state == targetReview {
				return res, nil
			}
			cand.state = targetFailed
			return cand, nil
		case document.VerdictRetry:
			if countChars(ex.Text()) > 0 {
				cand.state = targetReview
				res = cand
			} else if res.state == targetFailed {
				res.reason, res.quality = q.Reason, &q
			}
		}
	}
}

// runExtractor calls one extractor and records the attempt. Extractor
// failures are logged and reported as succeeded=false; a returned error is
// an infrastructure failure that should stop the run.
func (p *Pipeline) runExtractor(ctx context.Context, log *slog.Logger, j *document.Job, t document.Target, e Extractor, pdf []byte, attempt int, spend *document.Spend) (document.Extraction, bool, error) {
	started := p.now()
	actx, cancel := context.WithTimeout(ctx, p.cfg.AttemptTimeout)
	ex, extractErr := e.Extract(actx, ExtractRequest{PDF: pdf, Target: t, Attempt: attempt})
	cancel()

	a := document.Attempt{
		ID:               p.newID(),
		JobID:            j.ID(),
		TargetID:         t.ID,
		Extractor:        e.Name(),
		ExtractorVersion: e.Version(),
		Succeeded:        extractErr == nil,
		CostMicros:       ex.CostMicros,
		StartedAt:        started,
		FinishedAt:       p.now(),
	}
	if extractErr != nil {
		a.Error = truncate(extractErr.Error(), maxErrorRunes)
	}
	if err := p.attempts.RecordAttempt(ctx, a); err != nil {
		return ex, false, fmt.Errorf("record attempt: %w", err)
	}
	spend.Add(a)

	if extractErr != nil {
		if ctx.Err() != nil {
			return ex, false, ctx.Err()
		}
		log.WarnContext(ctx, "extractor failed", "target_id", t.ID, "extractor", e.Name(), "attempt_id", a.ID, "err", extractErr)
		return ex, false, nil
	}
	ex = stamp(ex, t, e, a.ID)
	if err := p.evidence.save(ctx, evidenceRef(j, "extract", extractionKey(t, e)), ex); err != nil {
		return ex, false, err
	}
	return ex, true, nil
}

// stamp sets provenance and direction on every block so published blocks
// reference their source page regardless of what the extractor returned.
func stamp(ex document.Extraction, t document.Target, e Extractor, attemptID string) document.Extraction {
	ex.TargetID, ex.Extractor, ex.ExtractorVersion = t.ID, e.Name(), e.Version()
	for i := range ex.Blocks {
		b := &ex.Blocks[i]
		if b.Page == 0 {
			b.Page = t.FirstPage()
		}
		b.Direction = detectDirection(b.PlainText())
		b.Provenance = document.Provenance{TargetID: t.ID, Extractor: e.Name(), ExtractorVersion: e.Version(), AttemptID: attemptID}
	}
	return ex
}

func (p *Pipeline) commit(ctx context.Context, log *slog.Logger, j *document.Job, a assembly, pages int, cacheKey string) (document.Status, error) {
	now := p.now().UTC()
	versionID := p.newID()
	contentRef := storage.Ref{Bucket: storage.BucketDerived, Name: path.Join(j.TenantID(), versionID, "content.json")}
	data, err := json.Marshal(document.Content{
		VersionID:       versionID,
		JobID:           j.ID(),
		Status:          a.outcome,
		Coverage:        a.coverage,
		PipelineVersion: p.version,
		Pages:           pages,
		Blocks:          a.blocks,
		Review:          a.review,
		Missing:         a.missing,
		CreatedAt:       now,
	})
	if err != nil {
		return "", fmt.Errorf("encode content: %w", err)
	}
	if err := p.objects.Write(ctx, contentRef, "application/json", data); err != nil {
		return "", fmt.Errorf("write content: %w", err)
	}

	if err := j.Complete(a.outcome, versionID, a.reason, now); err != nil {
		return "", err
	}
	c := Commit{
		Job:   j,
		Event: j.Event(p.newID(), now),
		Version: document.Version{
			ID:              versionID,
			TenantID:        j.TenantID(),
			JobID:           j.ID(),
			FileHash:        j.FileHash(),
			PipelineVersion: p.version,
			Status:          a.outcome,
			Coverage:        a.coverage,
			ContentObject:   contentRef.Name,
			OriginalObject:  j.OriginalObject(),
			CreatedAt:       now,
		},
		Quality: a.quality,
	}
	if a.outcome == document.StatusReady {
		c.CacheKey = cacheKey
	}
	if err := p.versions.CommitVersion(ctx, c); err != nil {
		return "", fmt.Errorf("commit version: %w", err)
	}
	log.InfoContext(ctx, "version committed", "version_id", versionID, "status", a.outcome, "coverage", a.coverage)
	return a.outcome, nil
}

func neighborContext(targets []document.Target, i int) string {
	var parts []string
	for _, n := range []int{i - 1, i + 1} {
		if n >= 0 && n < len(targets) && targets[n].TextLayer != "" {
			parts = append(parts, truncate(targets[n].TextLayer, maxContextRunes))
		}
	}
	return strings.Join(parts, "\n---\n")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
