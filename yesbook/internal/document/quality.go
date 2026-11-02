package document

// Verdict is the validation result for one extraction.
type Verdict string

const (
	VerdictAccept Verdict = "accept"
	VerdictRetry  Verdict = "retry"
	VerdictReview Verdict = "review"
	VerdictReject Verdict = "reject"
)

type Check struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Detail string  `json:"detail,omitempty"`
}

// QualityResult is the validation outcome stored for every target.
type QualityResult struct {
	TargetID         string  `json:"target_id"`
	Extractor        string  `json:"extractor"`
	ExtractorVersion string  `json:"extractor_version"`
	Verdict          Verdict `json:"verdict"`
	Checks           []Check `json:"checks"`
	Reason           string  `json:"reason,omitempty"`
}

// ReviewItem is a region that did not pass validation.
type ReviewItem struct {
	TargetID     string   `json:"target_id"`
	Pages        []int    `json:"pages"`
	Reason       string   `json:"reason"`
	Code         string   `json:"code,omitempty"`
	FailedChecks []string `json:"failed_checks,omitempty"`
}
