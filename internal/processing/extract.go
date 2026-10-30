package processing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
)

func (p *Pipeline) extractAll(
	ctx context.Context,
	log *slog.Logger,
	run progressRun,
	j *document.Job,
	pdf []byte,
	targets []document.Target,
) ([]targetResult, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StageExtract, status: ProgressStarted}); err != nil {
		return nil, err
	}
	spend, err := p.attempts.Spend(ctx, j.ID())
	if err != nil {
		return nil, p.failStages(ctx, run, fmt.Errorf("load spend: %w", err), StageExtract)
	}
	if err := p.record(ctx, run, progressUpdate{stage: StageValidate, status: ProgressStarted}); err != nil {
		return nil, p.failStages(ctx, run, err, StageExtract)
	}
	if err := p.noteDoclingPolicy(ctx, run, j, targets); err != nil {
		return nil, p.failStages(ctx, run, err, StageExtract, StageValidate)
	}
	results := make([]targetResult, 0, len(targets))
	for i := range targets {
		r, err := p.extractTarget(ctx, log, run, j, pdf, targets, i, &spend)
		if err != nil {
			return nil, p.failStages(ctx, run, err, StageExtract, StageValidate)
		}
		results = append(results, r)
		reason := ""
		if targets[i].Content == document.ContentBlank {
			reason = "blank page"
		}
		meta := countedProgress(i+1, len(targets), progressUnitTargets)
		if err := p.record(ctx, run, progressUpdate{
			stage:  StageExtract,
			status: ProgressAdvancing,
			reason: reason,
			meta:   meta,
		}); err != nil {
			return nil, p.failStages(ctx, run, err, StageExtract, StageValidate)
		}
		if err := p.record(ctx, run, progressUpdate{
			stage:  StageValidate,
			status: ProgressAdvancing,
			reason: reason,
			meta:   meta,
		}); err != nil {
			return nil, p.failStages(ctx, run, err, StageExtract, StageValidate)
		}
	}
	log.InfoContext(ctx, "extraction finished", "targets", len(targets), "attempts", spend.Attempts, "cost_micros", spend.CostMicros)
	if err := p.record(ctx, run, progressUpdate{stage: StageExtract, status: ProgressCompleted}); err != nil {
		return nil, p.failStages(ctx, run, err, StageExtract, StageValidate)
	}
	if err := p.record(ctx, run, progressUpdate{stage: StageValidate, status: ProgressCompleted}); err != nil {
		return nil, p.failStages(ctx, run, err, StageValidate)
	}
	return results, nil
}

// noteDoclingPolicy records a skipped path reason when Automatic work has
// embedded images but Docling is not configured. Escalation continues to any
// later extractors still in the chain (Gemini, OpenAI, …).
func (p *Pipeline) noteDoclingPolicy(ctx context.Context, run progressRun, j *document.Job, targets []document.Target) error {
	if j.ExtractionMode().Pinned() || p.router.HasDocling() {
		return nil
	}
	for _, t := range targets {
		if t.EmbeddedImages > 0 {
			return p.record(ctx, run, progressUpdate{
				stage:         StageExtract,
				status:        ProgressSkipped,
				reasonCode:    document.ReasonExtractorUnset,
				reasonMessage: "docling url unset; continuing Automatic escalate without it",
				reason:        "docling url unset; continuing Automatic escalate without it",
			})
		}
	}
	return nil
}

// noteChainExhausted records when the extractor chain ends without an accept so
// the timeline shows why Gemini/OpenAI were not tried (none configured) versus
// silent review.
func (p *Pipeline) noteChainExhausted(ctx context.Context, run progressRun, t document.Target, tried []string, res targetResult) error {
	if len(tried) == 0 {
		return p.record(ctx, run, progressUpdate{
			stage:         StageExtract,
			status:        ProgressSkipped,
			reasonCode:    document.ReasonChainExhausted,
			reasonMessage: fmt.Sprintf("no extractors configured for %s content", t.Content),
			reason:        fmt.Sprintf("no extractors configured for %s content", t.Content),
		})
	}
	if res.state != targetReview && res.state != targetFailed {
		return nil
	}
	msg := "no further extractors configured after " + strings.Join(tried, ", ")
	if res.reason != "" {
		msg = msg + "; " + res.reason
	}
	return p.record(ctx, run, progressUpdate{
		stage:         StageExtract,
		status:        ProgressSkipped,
		reasonCode:    document.ReasonChainExhausted,
		reasonMessage: msg,
		reason:        msg,
	})
}

// extractTarget routes one target through its extractor chain until an
// extraction passes validation, the chain runs out, or the budget is spent.
func (p *Pipeline) extractTarget(ctx context.Context, log *slog.Logger, run progressRun, j *document.Job, pdf []byte, targets []document.Target, i int, spend *document.Spend) (targetResult, error) {
	t := targets[i]
	mode := j.ExtractionMode()
	if t.Content == document.ContentBlank {
		q := document.QualityResult{TargetID: t.ID, Verdict: document.VerdictAccept, Reason: "blank page"}
		return targetResult{target: t, state: targetAccepted, extraction: &document.Extraction{TargetID: t.ID}, quality: &q}, nil
	}

	res := targetResult{target: t, state: targetFailed, reason: fmt.Sprintf("no extractor available for %s content", t.Content)}
	var tried []string
	for attempt := 1; ; attempt++ {
		e, ok := p.router.Next(t, tried, mode)
		if !ok {
			if err := p.noteChainExhausted(ctx, run, t, tried, res); err != nil {
				return res, err
			}
			return res, nil
		}
		tried = append(tried, e.Name())
		if attempt > 1 {
			t.Context = neighborContext(targets, i)
		}
		choose := ChooseReason(mode, attempt, e.Name())

		ex, pending, fromCache, succeeded, err := p.loadOrExtract(ctx, log, j, t, e, pdf, attempt, spend, choose)
		if err != nil {
			return res, err
		}
		if !fromCache && !succeeded {
			if reason, over := p.cfg.Budget.Exceeded(*spend, p.now()); over {
				if res.state == targetFailed {
					res.reason = reason
				}
				return res, nil
			}
			if res.state == targetFailed {
				res.reason = fmt.Sprintf("extractor %s failed", e.Name())
			}
			continue
		}
		if fromCache {
			_ = p.record(ctx, run, progressUpdate{
				stage:         StageExtract,
				status:        ProgressAdvancing,
				reasonCode:    choose.Code,
				reasonMessage: choose.Message + " (resumed)",
				reason:        choose.Message + " (resumed)",
				meta:          countedProgress(attempt, attempt, "attempts"),
			})
		}

		q := p.cfg.Validator.Validate(t, ex, p.router.HasNext(t, tried, mode))
		doclingForced := false
		if MustTryDocling(mode, t, e.Name(), tried, p.router.HasDocling()) && q.Verdict == document.VerdictAccept {
			doclingForced = true
			q.Verdict = document.VerdictRetry
			q.Reason = "Docling is required when Automatic processing finds embedded images"
			_ = p.record(ctx, run, progressUpdate{
				stage:         StageValidate,
				status:        ProgressSkipped,
				reasonCode:    document.ReasonDoclingRequired,
				reasonMessage: q.Reason,
				reason:        q.Reason,
			})
		}
		if !fromCache {
			annotateAttempt(&pending, choose, q, doclingForced)
			if err := p.attempts.RecordAttempt(ctx, pending); err != nil {
				return res, fmt.Errorf("record attempt: %w", err)
			}
			spend.Add(pending)
		}
		if err := p.evidence.save(ctx, evidenceRef(j, "validate", extractionKey(t, e)), q); err != nil {
			return res, fmt.Errorf("save validation: %w", err)
		}
		log.InfoContext(ctx, "target validated", "target_id", t.ID, "extractor", e.Name(), "verdict", q.Verdict, "reason", q.Reason)

		cand := targetResult{target: t, extraction: &ex, quality: &q, reason: q.Reason}
		switch q.Verdict {
		case document.VerdictAccept:
			cand.state = targetAccepted
			return cand, nil
		case document.VerdictReview:
			// canRetry was false: no later extractor is configured.
			cand.state = targetReview
			if err := p.noteChainExhausted(ctx, run, t, tried, cand); err != nil {
				return cand, err
			}
			return cand, nil
		case document.VerdictReject:
			if res.state == targetReview {
				if err := p.noteChainExhausted(ctx, run, t, tried, res); err != nil {
					return res, err
				}
				return res, nil
			}
			cand.state = targetFailed
			if err := p.noteChainExhausted(ctx, run, t, tried, cand); err != nil {
				return cand, err
			}
			return cand, nil
		case document.VerdictRetry:
			// Keep a review candidate when text exists, then escalate to the
			// next configured extractor (Gemini/OpenAI even if Docling was unset).
			if countChars(ex.Text()) > 0 {
				cand.state = targetReview
				res = cand
			} else if res.state == targetFailed {
				res.reason, res.quality = q.Reason, &q
			}
		}
	}
}

func (p *Pipeline) loadOrExtract(
	ctx context.Context,
	log *slog.Logger,
	j *document.Job,
	t document.Target,
	e Extractor,
	pdf []byte,
	attempt int,
	spend *document.Spend,
	choose document.PathReason,
) (document.Extraction, document.Attempt, bool, bool, error) {
	var ex document.Extraction
	found, err := p.evidence.load(ctx, evidenceRef(j, "extract", extractionKey(t, e)), &ex)
	if err != nil {
		return ex, document.Attempt{}, false, false, err
	}
	if found {
		return ex, document.Attempt{}, true, true, nil
	}
	if reason, over := p.cfg.Budget.Exceeded(*spend, p.now()); over {
		log.WarnContext(ctx, "budget exhausted", "target_id", t.ID, "reason", reason)
		// Caller stops the chain when spend already meets the budget.
		return ex, document.Attempt{}, false, false, nil
	}
	ex, a, succeeded, err := p.runExtractor(ctx, log, j, t, e, pdf, attempt, choose)
	if err != nil {
		return ex, a, false, false, err
	}
	if !succeeded {
		if err := p.attempts.RecordAttempt(ctx, a); err != nil {
			return ex, a, false, false, fmt.Errorf("record attempt: %w", err)
		}
		spend.Add(a)
		return ex, a, false, false, nil
	}
	return ex, a, false, true, nil
}

func annotateAttempt(a *document.Attempt, choose document.PathReason, q document.QualityResult, doclingForced bool) {
	if q.Verdict == document.VerdictAccept {
		a.Decision = document.DecisionChosen
		a.ReasonCode = document.ReasonValidationAccept
		a.ReasonMessage = "validation accepted " + a.Extractor
		return
	}
	a.Decision = document.DecisionDeclined
	if doclingForced {
		a.ReasonCode = document.ReasonDoclingRequired
		a.ReasonMessage = q.Reason
		return
	}
	reason := document.ReasonForFailedChecks(failedCheckNames(q.Checks))
	a.ReasonCode = reason.Code
	a.ReasonMessage = reason.Message
	if a.ReasonMessage == "" {
		a.ReasonMessage = q.Reason
	}
	if a.ReasonCode == "" {
		a.ReasonCode = choose.Code
		a.ReasonMessage = choose.Message
	}
}

// runExtractor calls one extractor. Successful attempts are recorded by the
// caller after validation so path codes can reflect chosen vs declined.
func (p *Pipeline) runExtractor(
	ctx context.Context,
	log *slog.Logger,
	j *document.Job,
	t document.Target,
	e Extractor,
	pdf []byte,
	attempt int,
	choose document.PathReason,
) (document.Extraction, document.Attempt, bool, error) {
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
		Decision:         document.DecisionChosen,
		ReasonCode:       choose.Code,
		ReasonMessage:    choose.Message,
	}
	if extractErr != nil {
		a.Error = truncate(extractErr.Error(), maxErrorRunes)
		a.Decision = document.DecisionDeclined
		a.ReasonCode = document.ReasonExtractorError
		a.ReasonMessage = "extractor error: " + truncate(extractErr.Error(), 200)
		if ctx.Err() != nil {
			return ex, a, false, ctx.Err()
		}
		log.WarnContext(ctx, "extractor failed", "target_id", t.ID, "extractor", e.Name(), "attempt_id", a.ID, "err", extractErr)
		return ex, a, false, nil
	}
	ex = stamp(ex, t, e, a.ID)
	if err := p.evidence.save(ctx, evidenceRef(j, "extract", extractionKey(t, e)), ex); err != nil {
		return ex, a, false, err
	}
	return ex, a, true, nil
}

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
