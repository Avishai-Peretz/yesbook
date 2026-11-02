package processing

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

type ProgressStage string

const (
	StageVerify      ProgressStage = "verify"
	StageCacheLookup ProgressStage = "cache_lookup"
	StagePrepare     ProgressStage = "prepare"
	StageExtract     ProgressStage = "extract"
	StageValidate    ProgressStage = "validate"
	StageAssemble    ProgressStage = "assemble"
	StagePublish     ProgressStage = "publish"
)

type ProgressStatus string

const (
	ProgressStarted   ProgressStatus = "started"
	ProgressAdvancing ProgressStatus = "progress"
	ProgressCompleted ProgressStatus = "completed"
	ProgressFailed    ProgressStatus = "failed"
	ProgressSkipped   ProgressStatus = "skipped"
)

const (
	MaxProgressUnitLength   = 32
	MaxProgressReasonLength = 2048
)

var ErrInvalidProgressEvent = errors.New("invalid progress event")

// ProgressMetadata is a bounded snapshot of work completed within a stage.
// Extraction attempts remain the source of extractor, cost and error details.
type ProgressMetadata struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Unit    string `json:"unit"`
}

// ProgressEvent is an append-only pipeline stage transition. Sequence is
// assigned by persistence and provides a stable timeline order.
type ProgressEvent struct {
	Sequence      int64             `json:"-"`
	ID            string            `json:"id"`
	TenantID      string            `json:"-"`
	JobID         string            `json:"document_id"`
	WorkflowID    string            `json:"workflow_id"`
	RunID         string            `json:"run_id"`
	Retry         int               `json:"retry"`
	Stage         ProgressStage     `json:"stage"`
	Status        ProgressStatus    `json:"event"`
	Progress      *ProgressMetadata `json:"progress,omitempty"`
	Reason        string            `json:"message,omitempty"`
	ReasonCode    string            `json:"reason_code,omitempty"`
	ReasonMessage string            `json:"reason_message,omitempty"`
	OccurredAt    time.Time         `json:"occurred_at"`
}

// ProgressSink is the write-side contract consumed by the processing
// pipeline. Adapters implement it without leaking persistence concerns here.
type ProgressSink interface {
	RecordProgress(ctx context.Context, event ProgressEvent) error
}

func (e ProgressEvent) Validate() error {
	switch {
	case e.ID == "":
		return invalidProgress("id is required")
	case e.TenantID == "":
		return invalidProgress("tenant id is required")
	case e.JobID == "":
		return invalidProgress("job id is required")
	case e.WorkflowID == "":
		return invalidProgress("workflow id is required")
	case e.RunID == "":
		return invalidProgress("run id is required")
	case e.Retry < 0:
		return invalidProgress("retry must not be negative")
	case !e.Stage.valid():
		return invalidProgress("unknown stage")
	case !e.Status.valid():
		return invalidProgress("unknown status")
	case e.OccurredAt.IsZero():
		return invalidProgress("occurred at is required")
	case utf8.RuneCountInString(e.Reason) > MaxProgressReasonLength:
		return invalidProgress("reason is too long")
	case utf8.RuneCountInString(e.ReasonMessage) > MaxProgressReasonLength:
		return invalidProgress("reason message is too long")
	case utf8.RuneCountInString(e.ReasonCode) > 64:
		return invalidProgress("reason code is too long")
	case e.Status == ProgressAdvancing && e.Progress == nil:
		return invalidProgress("progress metadata is required for progress status")
	case e.Progress != nil:
		return e.Progress.validate()
	default:
		return nil
	}
}

func (s ProgressStage) valid() bool {
	switch s {
	case StageVerify, StageCacheLookup, StagePrepare, StageExtract, StageValidate, StageAssemble, StagePublish:
		return true
	default:
		return false
	}
}

func (s ProgressStatus) valid() bool {
	switch s {
	case ProgressStarted, ProgressAdvancing, ProgressCompleted, ProgressFailed, ProgressSkipped:
		return true
	default:
		return false
	}
}

func (p ProgressMetadata) validate() error {
	switch {
	case p.Current < 0:
		return invalidProgress("progress current must not be negative")
	case p.Total <= 0:
		return invalidProgress("progress total must be positive")
	case p.Current > p.Total:
		return invalidProgress("progress current must not exceed total")
	case p.Unit == "":
		return invalidProgress("progress unit is required")
	case utf8.RuneCountInString(p.Unit) > MaxProgressUnitLength:
		return invalidProgress("progress unit is too long")
	default:
		return nil
	}
}

func invalidProgress(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidProgressEvent, reason)
}
