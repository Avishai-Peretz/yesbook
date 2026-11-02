package processing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
)

// Router picks the extractor for a target. Each content kind has an ordered
// chain: the first entry is the preferred extractor and later entries are
// escalations used when validation asks for a retry.
//
// Pin-then-escalate: when a job pins a tool, that extractor runs first (if
// configured), then the remaining Automatic chain for the content kind.
type Router struct {
	chains  map[document.ContentKind][]Extractor
	byName  map[string]Extractor
	docling bool
}

func NewRouter(chains map[document.ContentKind][]Extractor) *Router {
	byName := make(map[string]Extractor)
	docling := false
	for _, chain := range chains {
		for _, e := range chain {
			byName[e.Name()] = e
			if e.Name() == string(document.ModeDocling) {
				docling = true
			}
		}
	}
	return &Router{chains: chains, byName: byName, docling: docling}
}

// HasDocling reports whether Docling is configured in any chain.
func (r *Router) HasDocling() bool { return r.docling }

// HasExtractor reports whether name is available to pin or escalate to.
func (r *Router) HasExtractor(name string) bool {
	_, ok := r.byName[name]
	return ok
}

// Ordered returns the pin-then-escalate sequence for t under mode.
func (r *Router) Ordered(t document.Target, mode document.ExtractionMode) []Extractor {
	base := slices.Clone(r.chains[t.Content])
	if !mode.Pinned() {
		return base
	}
	pinned, ok := r.byName[mode.ExtractorName()]
	if !ok {
		return base
	}
	out := make([]Extractor, 0, len(base)+1)
	out = append(out, pinned)
	for _, e := range base {
		if e.Name() == pinned.Name() {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Next returns the first extractor for t that has not been tried yet.
func (r *Router) Next(t document.Target, tried []string, mode document.ExtractionMode) (Extractor, bool) {
	for _, e := range r.Ordered(t, mode) {
		if !slices.Contains(tried, e.Name()) {
			return e, true
		}
	}
	return nil, false
}

func (r *Router) HasNext(t document.Target, tried []string, mode document.ExtractionMode) bool {
	_, ok := r.Next(t, tried, mode)
	return ok
}

// ChooseReason returns the stable code for selecting extractor on this try.
func ChooseReason(mode document.ExtractionMode, attempt int, extractor string) document.PathReason {
	switch {
	case attempt > 1:
		return document.PathReason{Code: document.ReasonEscalated, Message: "escalating to " + extractor}
	case mode.Pinned() && extractor == mode.ExtractorName():
		return document.PathReason{Code: document.ReasonPinnedStart, Message: "starting with pinned " + extractor}
	default:
		return document.PathReason{Code: document.ReasonChainFirst, Message: "starting Automatic chain with " + extractor}
	}
}

// Signature lists every chain with pinned extractor versions, in a stable order.
func (r *Router) Signature() string {
	var sb strings.Builder
	for _, kind := range slices.Sorted(maps.Keys(r.chains)) {
		fmt.Fprintf(&sb, "%s=", kind)
		for i, e := range r.chains[kind] {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%s@%s", e.Name(), e.Version())
		}
		sb.WriteByte(';')
	}
	return sb.String()
}

// pipelineVersion is a short hash of everything that changes the output for
// the same input file.
func pipelineVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "p-" + hex.EncodeToString(sum[:6])
}
