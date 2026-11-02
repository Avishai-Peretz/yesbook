package processing

import (
	"fmt"
	"slices"

	"github.com/avi-p/yesbook/internal/document"
)

type targetState int

const (
	targetFailed targetState = iota + 1
	targetReview
	targetAccepted
)

// targetResult is the final state of one target after routing and retries.
type targetResult struct {
	target     document.Target
	state      targetState
	extraction *document.Extraction
	quality    *document.QualityResult
	reason     string
}

type assembly struct {
	outcome  document.Status
	reason   string
	coverage float64
	blocks   []document.Block
	review   []document.ReviewItem
	missing  []document.ReviewItem
	quality  []document.QualityResult
}

// assemble joins accepted blocks in page order, merges tables that continue
// across pages, and decides the document status from target coverage.
func assemble(results []targetResult, minPartialCoverage float64) assembly {
	a, accepted := collectResults(results)
	a.blocks = finalizeBlocks(a.blocks)
	a.outcome, a.reason = decideOutcome(len(results), accepted, len(a.review), len(a.missing), a.coverage, minPartialCoverage)
	return a
}

func collectResults(results []targetResult) (assembly, int) {
	var a assembly
	accepted := 0
	for _, r := range results {
		if r.quality != nil {
			a.quality = append(a.quality, *r.quality)
		}
		item := reviewItemFrom(r)
		switch r.state {
		case targetAccepted:
			accepted++
			if r.extraction != nil {
				a.blocks = append(a.blocks, r.extraction.Blocks...)
			}
		case targetReview:
			a.review = append(a.review, item)
			// Keep the best candidate blocks so reviewers can see what was extracted.
			if r.extraction != nil {
				a.blocks = append(a.blocks, r.extraction.Blocks...)
			}
		case targetFailed:
			a.missing = append(a.missing, item)
		}
	}
	if len(results) > 0 {
		a.coverage = float64(accepted) / float64(len(results))
	}
	return a, accepted
}

func reviewItemFrom(r targetResult) document.ReviewItem {
	item := document.ReviewItem{TargetID: r.target.ID, Pages: r.target.Pages, Reason: r.reason}
	if r.quality == nil {
		return item
	}
	failed := failedCheckNames(r.quality.Checks)
	if len(failed) > 0 {
		item.FailedChecks = failed
		reason := document.ReasonForFailedChecks(failed)
		item.Code = reason.Code
		if item.Reason == "" {
			item.Reason = reason.Message
		}
	}
	if item.Reason == "" {
		item.Reason = r.quality.Reason
	}
	return item
}

func finalizeBlocks(blocks []document.Block) []document.Block {
	slices.SortStableFunc(blocks, func(x, y document.Block) int { return x.Page - y.Page })
	return mergeTables(blocks)
}

func decideOutcome(total, accepted, review, missing int, coverage, minPartialCoverage float64) (document.Status, string) {
	switch {
	case total == 0:
		return document.StatusBlocked, "document has no pages"
	case accepted == total:
		return document.StatusReady, ""
	case review > 0:
		return document.StatusNeedsReview, fmt.Sprintf("%d of %d pages flagged for review", review, total)
	case accepted > 0 && coverage >= minPartialCoverage:
		return document.StatusPartial, fmt.Sprintf("%d of %d pages could not be extracted", missing, total)
	default:
		return document.StatusBlocked, fmt.Sprintf("only %d of %d pages passed validation", accepted, total)
	}
}

// mergeTables joins a table that ends one page with a table that starts the
// next page when both have the same number of columns, dropping a repeated
// header row.
func mergeTables(blocks []document.Block) []document.Block {
	out := make([]document.Block, 0, len(blocks))
	for _, b := range blocks {
		if n := len(out); n > 0 && continuesTable(out[n-1], b) {
			prev := &out[n-1]
			rows := b.Rows
			if slices.Equal(rows[0], prev.Rows[0]) {
				rows = rows[1:]
			}
			prev.Rows = append(prev.Rows, rows...)
			prev.EndPage = b.Page
			continue
		}
		out = append(out, b)
	}
	return out
}

func continuesTable(prev, next document.Block) bool {
	if prev.Kind != document.BlockTable || next.Kind != document.BlockTable || len(prev.Rows) == 0 || len(next.Rows) == 0 {
		return false
	}
	lastPage := max(prev.Page, prev.EndPage)
	return next.Page == lastPage+1 && len(prev.Rows[0]) == len(next.Rows[0])
}
