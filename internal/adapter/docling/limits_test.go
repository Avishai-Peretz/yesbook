package docling

import (
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/processing"
)

func TestReadLimited(t *testing.T) {
	data, err := readLimited(strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("data = %q, err = %v", data, err)
	}
	_, err = readLimited(strings.NewReader("12345"), 4)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v", err)
	}
}

func TestFormRequiresPages(t *testing.T) {
	_, _, err := form(processing.ExtractRequest{})
	if err == nil {
		t.Fatal("empty pages should be rejected")
	}
}
