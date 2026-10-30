// Package modes reports which extraction tools are configured from the
// environment without constructing SDK clients, so API binaries can validate
// extraction_mode without linking model SDKs.
package modes

import (
	"fmt"
	"os"
	"strings"

	"github.com/avi-p/yesbook/internal/document"
)

// Configured returns the set of tool names that Register/Submit may pin.
// PDFium is always available. Automatic is always allowed separately.
func Configured(getenv func(string) string) map[string]bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	out := map[string]bool{
		string(document.ModePDFium): true,
	}
	if strings.TrimSpace(getenv("YESBOOK_DOCLING_URL")) != "" {
		out[string(document.ModeDocling)] = true
	}
	if strings.TrimSpace(getenv("YESBOOK_GEMINI_API_KEY")) != "" ||
		(strings.TrimSpace(getenv("YESBOOK_PROJECT")) != "" && strings.TrimSpace(getenv("YESBOOK_GEMINI_LOCATION")) != "") {
		out[string(document.ModeGemini)] = true
	}
	if strings.TrimSpace(getenv("YESBOOK_OPENAI_API_KEY")) != "" {
		out[string(document.ModeOpenAI)] = true
	}
	return out
}

// Validate reports whether mode is known and, when pinned, configured.
func Validate(mode document.ExtractionMode, configured map[string]bool) error {
	if mode == "" || mode == document.ModeAutomatic {
		return nil
	}
	parsed, err := document.ParseExtractionMode(string(mode))
	if err != nil {
		return err
	}
	if !parsed.Pinned() {
		return nil
	}
	if configured == nil || !configured[parsed.ExtractorName()] {
		return fmt.Errorf("%w: extraction_mode %q is not configured", document.ErrInvalid, parsed)
	}
	return nil
}
