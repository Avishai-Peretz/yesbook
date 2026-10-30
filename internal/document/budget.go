package document

import "time"

// Attempt is one extractor call for one target.
type Attempt struct {
	ID               string
	JobID            string
	TargetID         string
	Extractor        string
	ExtractorVersion string
	Succeeded        bool
	Error            string
	CostMicros       int64
	StartedAt        time.Time
	FinishedAt       time.Time
	ReasonCode       string
	ReasonMessage    string
	Decision         PathDecisionKind
}

// Spend is what a job has consumed so far, across worker restarts.
type Spend struct {
	Attempts       int
	CostMicros     int64
	FirstAttemptAt time.Time
}

// Add accounts for a finished attempt.
func (s *Spend) Add(a Attempt) {
	s.Attempts++
	s.CostMicros += a.CostMicros
	if s.FirstAttemptAt.IsZero() {
		s.FirstAttemptAt = a.StartedAt
	}
}

// Budget caps the attempts, time and cost spent on one document. A zero
// field means no limit.
type Budget struct {
	MaxAttempts   int
	MaxDuration   time.Duration
	MaxCostMicros int64
}

// Exceeded reports whether s has used up the budget, and which limit.
func (b Budget) Exceeded(s Spend, now time.Time) (string, bool) {
	switch {
	case b.MaxAttempts > 0 && s.Attempts >= b.MaxAttempts:
		return "attempt limit reached", true
	case b.MaxCostMicros > 0 && s.CostMicros >= b.MaxCostMicros:
		return "cost limit reached", true
	case b.MaxDuration > 0 && !s.FirstAttemptAt.IsZero() && now.Sub(s.FirstAttemptAt) >= b.MaxDuration:
		return "time limit reached", true
	default:
		return "", false
	}
}
