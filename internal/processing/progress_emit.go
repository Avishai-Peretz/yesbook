package processing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

const failRecordTimeout = 5 * time.Second

const (
	progressUnitPages     = "pages"
	progressUnitTargets   = "targets"
	progressUnitLookups   = "lookups"
	progressUnitDocuments = "documents"

	cachedVersionReason = "identical file already processed"
)

// progressStageOrder is the production timeline. Later stages are skipped
// when an earlier stage ends the run.
var progressStageOrder = []ProgressStage{
	StageVerify,
	StageCacheLookup,
	StagePrepare,
	StageExtract,
	StageValidate,
	StageAssemble,
	StagePublish,
}

type runIdentityKey struct{}

// RunIdentity is optional workflow, run and retry metadata for progress events.
// Process uses the worker id, a new run id and retry 0 when the corresponding
// field is empty.
type RunIdentity struct {
	WorkflowID string
	RunID      string
	Retry      int
}

// WithRunIdentity attaches workflow, run and retry metadata to ctx.
// Process rejects a negative retry.
func WithRunIdentity(ctx context.Context, id RunIdentity) context.Context {
	return context.WithValue(ctx, runIdentityKey{}, id)
}

// progressRun is the identity copied onto every event in one Process call.
type progressRun struct {
	tenantID   string
	jobID      string
	workflowID string
	runID      string
	retry      int
}

type progressUpdate struct {
	stage         ProgressStage
	status        ProgressStatus
	reason        string
	reasonCode    string
	reasonMessage string
	meta          *ProgressMetadata
}

func (p *Pipeline) progressRun(ctx context.Context, tenantID, jobID, worker string) (progressRun, error) {
	id, hasID := ctx.Value(runIdentityKey{}).(RunIdentity)
	if hasID && id.Retry < 0 {
		return progressRun{}, fmt.Errorf("%w: retry must not be negative", ErrInvalidProgressEvent)
	}
	run := progressRun{
		tenantID:   tenantID,
		jobID:      jobID,
		workflowID: worker,
		runID:      p.newID(),
	}
	if !hasID {
		return run, nil
	}
	if id.WorkflowID != "" {
		run.workflowID = id.WorkflowID
	}
	if id.RunID != "" {
		run.runID = id.RunID
	}
	run.retry = id.Retry
	return run, nil
}

func (p *Pipeline) record(ctx context.Context, run progressRun, up progressUpdate) error {
	if p.progress == nil {
		return nil
	}
	reasonMessage := up.reasonMessage
	if reasonMessage == "" {
		reasonMessage = up.reason
	}
	event := ProgressEvent{
		ID:            p.newID(),
		TenantID:      run.tenantID,
		JobID:         run.jobID,
		WorkflowID:    run.workflowID,
		RunID:         run.runID,
		Retry:         run.retry,
		Stage:         up.stage,
		Status:        up.status,
		Reason:        up.reason,
		ReasonCode:    up.reasonCode,
		ReasonMessage: reasonMessage,
		OccurredAt:    p.now().UTC(),
	}
	if up.meta != nil {
		meta := *up.meta
		event.Progress = &meta
	}
	if err := p.progress.RecordProgress(ctx, event); err != nil {
		return fmt.Errorf("record progress: %w", err)
	}
	return nil
}

// failStages stores a failed event for each open stage, then returns cause.
// The write uses a short detached context so a retryable failure can still be
// recorded after cancellation without hanging shutdown indefinitely.
func (p *Pipeline) failStages(ctx context.Context, run progressRun, cause error, stages ...ProgressStage) error {
	if cause == nil {
		return nil
	}
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failRecordTimeout)
	defer cancel()
	reason := truncate(cause.Error(), MaxProgressReasonLength)
	var recErr error
	for _, stage := range stages {
		if err := p.record(recCtx, run, progressUpdate{
			stage:  stage,
			status: ProgressFailed,
			reason: reason,
		}); err != nil {
			recErr = errors.Join(recErr, fmt.Errorf("record %s failure: %w", stage, err))
		}
	}
	if recErr != nil {
		return errors.Join(cause, recErr)
	}
	return cause
}

// skipThrough records skipped for every stage after "after" through "last".
// reasonCode may be empty; when set it is copied onto every skipped event.
func (p *Pipeline) skipThrough(ctx context.Context, run progressRun, after, last ProgressStage, reason, reasonCode string) error {
	if after == last {
		return nil
	}
	reason = truncate(reason, MaxProgressReasonLength)
	skipping := false
	for _, stage := range progressStageOrder {
		if !skipping {
			if stage == after {
				skipping = true
			}
			continue
		}
		if err := p.record(ctx, run, progressUpdate{
			stage:         stage,
			status:        ProgressSkipped,
			reason:        reason,
			reasonCode:    reasonCode,
			reasonMessage: reason,
		}); err != nil {
			return err
		}
		if stage == last {
			return nil
		}
	}
	return nil
}

func countedProgress(current, total int, unit string) *ProgressMetadata {
	return &ProgressMetadata{Current: current, Total: total, Unit: unit}
}

func (p *Pipeline) lookupCache(ctx context.Context, run progressRun, cacheKey string) (string, bool, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StageCacheLookup, status: ProgressStarted}); err != nil {
		return "", false, err
	}
	cached, ok, err := p.versions.FindCachedVersion(ctx, cacheKey)
	if err != nil {
		return "", false, p.failStages(ctx, run, fmt.Errorf("look up cached version: %w", err), StageCacheLookup)
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StageCacheLookup,
		status: ProgressAdvancing,
		meta:   countedProgress(1, 1, progressUnitLookups),
	}); err != nil {
		return "", false, p.failStages(ctx, run, err, StageCacheLookup)
	}
	reason := ""
	reasonCode := ""
	versionID := ""
	if ok {
		reason = cachedVersionReason
		reasonCode = document.ReasonHashMatch
		versionID = cached.ID
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:         StageCacheLookup,
		status:        ProgressCompleted,
		reason:        reason,
		reasonCode:    reasonCode,
		reasonMessage: reason,
	}); err != nil {
		return "", false, p.failStages(ctx, run, err, StageCacheLookup)
	}
	return versionID, ok, nil
}

func (p *Pipeline) recordAssembly(ctx context.Context, run progressRun, results []targetResult, a assembly) error {
	if err := p.record(ctx, run, progressUpdate{stage: StageAssemble, status: ProgressStarted}); err != nil {
		return err
	}
	if total := len(results); total > 0 {
		if err := p.record(ctx, run, progressUpdate{
			stage:  StageAssemble,
			status: ProgressAdvancing,
			meta:   countedProgress(acceptedTargets(results), total, progressUnitTargets),
		}); err != nil {
			return p.failStages(ctx, run, err, StageAssemble)
		}
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StageAssemble,
		status: ProgressCompleted,
		reason: a.reason,
	}); err != nil {
		return p.failStages(ctx, run, err, StageAssemble)
	}
	return nil
}

func acceptedTargets(results []targetResult) int {
	n := 0
	for _, r := range results {
		if r.state == targetAccepted {
			n++
		}
	}
	return n
}

// publishFinished records publish around a job completion that reuses an
// existing version. A recording error after the job is final is returned
// without a failed event, because the publish itself succeeded.
func (p *Pipeline) publishFinished(
	ctx context.Context,
	log *slog.Logger,
	run progressRun,
	j *document.Job,
	outcome document.Status,
	versionID, reason string,
) (document.Status, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StagePublish, status: ProgressStarted}); err != nil {
		return "", err
	}
	status, err := p.finish(ctx, log, j, outcome, versionID, reason)
	if err != nil {
		return "", p.failStages(ctx, run, err, StagePublish)
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StagePublish,
		status: ProgressAdvancing,
		meta:   countedProgress(1, 1, progressUnitDocuments),
	}); err != nil {
		return "", err
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StagePublish,
		status: ProgressCompleted,
		reason: reason,
	}); err != nil {
		return "", err
	}
	return status, nil
}
