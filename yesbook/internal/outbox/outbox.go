// Package outbox defines events written in the same transaction as the state
// change they describe, for later publication to Pub/Sub.
package outbox

import (
	"time"

	"github.com/avi-p/yesbook/internal/job"
)

const (
	TypeJobRegistered = "job.registered"
	TypeJobQueued     = "job.queued"
)

// Event is one outbox record.
type Event struct {
	ID          string
	Type        string
	AggregateID string
	TenantID    string
	Payload     Payload
	CreatedAt   time.Time
}

// Payload carries IDs and status only; consumers read content through the
// Content API.
type Payload struct {
	JobID    string     `json:"job_id"`
	TenantID string     `json:"tenant_id"`
	Status   job.Status `json:"status"`
}

// ForJob returns an event describing j's current status.
func ForJob(eventID, eventType string, j job.Job, now time.Time) Event {
	return Event{
		ID:          eventID,
		Type:        eventType,
		AggregateID: j.ID,
		TenantID:    j.TenantID,
		Payload:     Payload{JobID: j.ID, TenantID: j.TenantID, Status: j.Status},
		CreatedAt:   now,
	}
}
