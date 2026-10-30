package document

import (
	"fmt"
	"strings"
)

// ExtractionMode selects Automatic routing or a pinned extractor that runs
// first and then escalates like Automatic (pin-then-escalate).
type ExtractionMode string

const (
	ModeAutomatic ExtractionMode = "automatic"
	ModePDFium    ExtractionMode = "pdfium"
	ModeDocling   ExtractionMode = "docling"
	ModeGemini    ExtractionMode = "gemini"
	ModeOpenAI    ExtractionMode = "openai"
)

// KnownExtractionModes are the modes clients may send. Tools beyond this
// list are rejected as unknown even if an adapter exists.
var KnownExtractionModes = []ExtractionMode{
	ModeAutomatic,
	ModePDFium,
	ModeDocling,
	ModeGemini,
	ModeOpenAI,
}

// ParseExtractionMode normalizes raw. Empty becomes Automatic.
func ParseExtractionMode(raw string) (ExtractionMode, error) {
	mode := ExtractionMode(strings.TrimSpace(strings.ToLower(raw)))
	if mode == "" {
		return ModeAutomatic, nil
	}
	for _, known := range KnownExtractionModes {
		if mode == known {
			return mode, nil
		}
	}
	return "", fmt.Errorf("%w: unknown extraction_mode %q", ErrInvalid, raw)
}

// Pinned reports whether mode names a specific extractor rather than Automatic.
func (m ExtractionMode) Pinned() bool {
	return m != "" && m != ModeAutomatic
}

// ExtractorName is the router name for a pinned mode.
func (m ExtractionMode) ExtractorName() string {
	if !m.Pinned() {
		return ""
	}
	return string(m)
}
