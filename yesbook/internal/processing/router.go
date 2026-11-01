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
type Router struct {
	chains map[document.ContentKind][]Extractor
}

func NewRouter(chains map[document.ContentKind][]Extractor) *Router {
	return &Router{chains: chains}
}

// Next returns the first extractor for t that has not been tried yet.
func (r *Router) Next(t document.Target, tried []string) (Extractor, bool) {
	for _, e := range r.chains[t.Content] {
		if !slices.Contains(tried, e.Name()) {
			return e, true
		}
	}
	return nil, false
}

func (r *Router) HasNext(t document.Target, tried []string) bool {
	_, ok := r.Next(t, tried)
	return ok
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
