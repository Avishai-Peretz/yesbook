package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

// policyVersion changes whenever routing or assembly rules change in a way
// that should invalidate cached versions.
const policyVersion = "2026-11-02-mode-assets"

const (
	minTextLayerChars = 20
	maxContextRunes   = 2000
	maxErrorRunes     = 500
)

type Dependencies struct {
	Jobs      JobRepository
	Versions  VersionRepository
	Attempts  AttemptLog
	Progress  ProgressSink
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
		// Keep the lease above the Cloud Workflows / local runner deadline
		// so a healthy worker is not reclaimed mid-run.
		Lease:              35 * time.Minute,
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
	progress  ProgressSink
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
		progress:  d.Progress,
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
	run, err := p.progressRun(ctx, j.TenantID(), j.ID(), worker)
	if err != nil {
		return "", err
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

	pdf, info, reason, err := p.verify(ctx, run, j)
	if err != nil {
		return "", err
	}
	if reason != "" {
		if err := p.skipThrough(ctx, run, StageVerify, StagePublish, reason, ""); err != nil {
			return "", err
		}
		return p.finish(ctx, log, j, document.StatusBlocked, "", reason)
	}

	cacheKey := document.CacheKey(j.TenantID(), j.FileHash(), p.version)
	versionID, hit, err := p.lookupCache(ctx, run, cacheKey)
	if err != nil {
		return "", err
	}
	if hit {
		log.InfoContext(ctx, "hash match, returning existing version", "version_id", versionID)
		if err := p.skipThrough(ctx, run, StageCacheLookup, StageAssemble, cachedVersionReason, document.ReasonHashMatch); err != nil {
			return "", err
		}
		return p.publishFinished(ctx, log, run, j, document.StatusReady, versionID, cachedVersionReason)
	}

	targets, err := p.prepare(ctx, run, j, info)
	if err != nil {
		return "", err
	}
	results, err := p.extractAll(ctx, log, run, j, pdf, targets)
	if err != nil {
		return "", err
	}
	a := assemble(results, p.cfg.MinPartialCoverage)
	if err := p.recordAssembly(ctx, run, results, a); err != nil {
		return "", err
	}
	if a.outcome == document.StatusBlocked {
		if err := p.skipThrough(ctx, run, StageAssemble, StagePublish, a.reason, ""); err != nil {
			return "", err
		}
		return p.finish(ctx, log, j, document.StatusBlocked, "", a.reason)
	}
	return p.commit(ctx, log, run, j, a, len(targets), cacheKey)
}

// Block marks a job blocked after the workflow gave up on it.
// worker must be the execution that is giving up; a processing job owned by
// another worker whose lease has not expired is left alone so a losing
// duplicate execution cannot cancel a healthy run.
func (p *Pipeline) Block(ctx context.Context, tenantID, jobID, worker, reason string) error {
	j, err := p.jobs.GetJob(ctx, tenantID, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}
	if j.Status().Final() {
		return nil
	}
	now := p.now().UTC()
	if j.Status() == document.StatusProcessing {
		owned := worker != "" && j.ClaimedBy() == worker
		leaseExpired := now.Sub(j.UpdatedAt()) > p.cfg.Lease
		if !owned && !leaseExpired {
			p.log.InfoContext(ctx, "skipping block; job owned by another worker",
				"tenant_id", tenantID, "job_id", jobID, "worker", worker, "claimed_by", j.ClaimedBy())
			return nil
		}
	}
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
