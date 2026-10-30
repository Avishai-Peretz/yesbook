package document

import "time"

// Event announces a job status change. It carries IDs and status only;
// consumers read content through the Content API.
type Event struct {
	ID         string    `json:"event_id"`
	Type       string    `json:"event_type"`
	JobID      string    `json:"job_id"`
	TenantID   string    `json:"tenant_id"`
	VersionID  string    `json:"version_id,omitempty"`
	Status     Status    `json:"status"`
	OccurredAt time.Time `json:"occurred_at"`
}
