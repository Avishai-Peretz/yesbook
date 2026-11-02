package processing_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/avi-p/yesbook/internal/processing"
)

func TestProgressEventJSONContract(t *testing.T) {
	event := validProgressEvent()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"event-1","document_id":"job-1","workflow_id":"workflow-1",` +
		`"run_id":"run-2","retry":2,"stage":"extract","event":"progress",` +
		`"progress":{"current":3,"total":8,"unit":"targets"},"message":"page batch complete",` +
		`"occurred_at":"2026-11-02T12:00:00Z"}`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
}

func TestProgressEventValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*processing.ProgressEvent)
	}{
		{name: "missing id", change: func(event *processing.ProgressEvent) { event.ID = "" }},
		{name: "unknown stage", change: func(event *processing.ProgressEvent) { event.Stage = "unknown" }},
		{name: "unknown status", change: func(event *processing.ProgressEvent) { event.Status = "unknown" }},
		{name: "negative retry", change: func(event *processing.ProgressEvent) { event.Retry = -1 }},
		{name: "progress missing metadata", change: func(event *processing.ProgressEvent) { event.Progress = nil }},
		{name: "current exceeds total", change: func(event *processing.ProgressEvent) {
			event.Progress.Current = event.Progress.Total + 1
		}},
		{name: "reason too long", change: func(event *processing.ProgressEvent) {
			event.Reason = strings.Repeat("x", processing.MaxProgressReasonLength+1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := validProgressEvent()
			tt.change(&event)
			if err := event.Validate(); !errors.Is(err, processing.ErrInvalidProgressEvent) {
				t.Fatalf("Validate() = %v, want ErrInvalidProgressEvent", err)
			}
		})
	}
}

func validProgressEvent() processing.ProgressEvent {
	return processing.ProgressEvent{
		ID:         "event-1",
		TenantID:   "tenant-a",
		JobID:      "job-1",
		WorkflowID: "workflow-1",
		RunID:      "run-2",
		Retry:      2,
		Stage:      processing.StageExtract,
		Status:     processing.ProgressAdvancing,
		Progress: &processing.ProgressMetadata{
			Current: 3,
			Total:   8,
			Unit:    "targets",
		},
		Reason:     "page batch complete",
		OccurredAt: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
	}
}
