package document

import "time"

// PathDecisionKind is how the router treated an extractor or policy step.
type PathDecisionKind string

const (
	DecisionChosen   PathDecisionKind = "chosen"
	DecisionDeclined PathDecisionKind = "declined"
	DecisionSkipped  PathDecisionKind = "skipped"
)

// Stable path reason codes. Messages stay short and human-readable; codes are
// what clients match on.
const (
	ReasonChainFirst       = "chain_first"
	ReasonPinnedStart      = "pinned_start"
	ReasonEscalated        = "escalated"
	ReasonFiguresMissing   = "figures_missing"
	ReasonLayoutMissing    = "layout_missing"
	ReasonValidationAccept = "validation_accept"
	ReasonExtractorUnset   = "extractor_unset"
	ReasonDoclingRequired  = "docling_required"
	ReasonHashMatch        = "hash_match"
	ReasonCoverageLow      = "coverage_low"
	ReasonExtractorError   = "extractor_error"
	ReasonChainExhausted   = "chain_exhausted"
)

// PathReason is a stable machine code plus a short message.
type PathReason struct {
	Code    string
	Message string
}

// PathDecision is one chosen/declined/skipped step exposed on the timeline.
type PathDecision struct {
	Kind          PathDecisionKind `json:"kind"`
	ReasonCode    string           `json:"reason_code"`
	ReasonMessage string           `json:"reason_message"`
	Extractor     string           `json:"extractor,omitempty"`
	TargetID      string           `json:"target_id,omitempty"`
	OccurredAt    time.Time        `json:"occurred_at,omitempty"`
}

// ReasonForFailedChecks maps validation check names to a stable code.
func ReasonForFailedChecks(failed []string) PathReason {
	for _, name := range failed {
		switch name {
		case "figures":
			return PathReason{Code: ReasonFiguresMissing, Message: "embedded images were not represented as figures"}
		case "layout":
			return PathReason{Code: ReasonLayoutMissing, Message: "blocks are missing layout positions"}
		case "coverage":
			return PathReason{Code: ReasonCoverageLow, Message: "extracted text coverage is below the threshold"}
		}
	}
	if len(failed) == 0 {
		return PathReason{Code: ReasonValidationAccept, Message: "validation accepted the extraction"}
	}
	return PathReason{Code: ReasonEscalated, Message: "validation failed: " + failed[0]}
}
