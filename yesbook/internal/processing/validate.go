package processing

import (
	"fmt"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
)

// Validator checks one extraction for coverage, structure, Hebrew text
// direction and agreement with the embedded text layer.
type Validator struct {
	MinCoverage   float64
	MinAgreement  float64
	MinLegibility float64
}

func DefaultValidator() Validator {
	return Validator{MinCoverage: 0.85, MinAgreement: 0.5, MinLegibility: 0.95}
}

func (v Validator) String() string {
	return fmt.Sprintf("coverage>=%.2f agreement>=%.2f legibility>=%.2f", v.MinCoverage, v.MinAgreement, v.MinLegibility)
}

// Validate returns accept when every check passes. Otherwise it asks for a
// retry while another extractor is available, then falls back to review,
// or reject when there is no usable text at all.
func (v Validator) Validate(t document.Target, ex document.Extraction, canRetry bool) document.QualityResult {
	text := ex.Text()
	checks := []document.Check{
		v.coverage(t, text),
		structure(t, ex),
		threshold("legibility", legibility(text), v.MinLegibility),
	}
	if t.Language == "he" || detectLanguage(text) == "he" {
		checks = append(checks, direction(text))
	}
	if t.TextLayerChars > 0 {
		checks = append(checks, threshold("text_layer_agreement", agreement(t.TextLayer, text), v.MinAgreement))
	}

	var failed []string
	for _, c := range checks {
		if !c.Passed {
			failed = append(failed, c.Name)
		}
	}
	q := document.QualityResult{
		TargetID:         t.ID,
		Extractor:        ex.Extractor,
		ExtractorVersion: ex.ExtractorVersion,
		Checks:           checks,
	}
	switch {
	case len(failed) == 0:
		q.Verdict = document.VerdictAccept
		return q
	case canRetry:
		q.Verdict = document.VerdictRetry
	case countChars(text) == 0:
		q.Verdict = document.VerdictReject
	default:
		q.Verdict = document.VerdictReview
	}
	q.Reason = "failed checks: " + strings.Join(failed, ", ")
	return q
}

func (v Validator) coverage(t document.Target, text string) document.Check {
	got := countChars(text)
	if t.TextLayerChars == 0 {
		score := 0.0
		if got > 0 {
			score = 1
		}
		return document.Check{Name: "coverage", Passed: score == 1, Score: score, Detail: fmt.Sprintf("%d characters, no text layer", got)}
	}
	score := min(float64(got)/float64(t.TextLayerChars), 1)
	return document.Check{
		Name:   "coverage",
		Passed: score >= v.MinCoverage,
		Score:  score,
		Detail: fmt.Sprintf("%d of %d text layer characters", got, t.TextLayerChars),
	}
}

// structure checks that every block is anchored to one of the target's
// pages and that tables are rectangular.
func structure(t document.Target, ex document.Extraction) document.Check {
	for i, b := range ex.Blocks {
		if !t.Contains(b.Page) {
			return document.Check{Name: "structure", Detail: fmt.Sprintf("block %d is anchored to page %d outside the target", i, b.Page)}
		}
		if b.Kind == document.BlockTable {
			for _, row := range b.Rows {
				if len(row) != len(b.Rows[0]) {
					return document.Check{Name: "structure", Detail: fmt.Sprintf("table block %d has ragged rows", i)}
				}
			}
		} else if strings.TrimSpace(b.Text) == "" && b.Kind != document.BlockFigure {
			return document.Check{Name: "structure", Detail: fmt.Sprintf("block %d is empty", i)}
		}
	}
	return document.Check{Name: "structure", Passed: true, Score: 1}
}

func direction(text string) document.Check {
	score, ok := hebrewOrderScore(text)
	c := document.Check{Name: "hebrew_direction", Passed: score >= 0.5, Score: score}
	if !ok {
		c.Detail = "too few final letter forms to judge"
	} else if !c.Passed {
		c.Detail = "Hebrew text appears to be in visual (reversed) order"
	}
	return c
}

func threshold(name string, score, minimum float64) document.Check {
	return document.Check{Name: name, Passed: score >= minimum, Score: score}
}
