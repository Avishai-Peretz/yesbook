package pdfium_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"testing"

	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

// buildPDF writes a minimal valid PDF with one page per text, using the
// Helvetica base font. An empty text gives a blank page.
func buildPDF(texts ...string) []byte {
	var objs []string
	kids := make([]string, len(texts))
	for i, text := range texts {
		pageID, contentID := 4+2*i, 5+2*i
		kids[i] = fmt.Sprintf("%d 0 R", pageID)
		stream := ""
		if text != "" {
			stream = fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", text)
		}
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentID),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		)
	}
	objs = append([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(texts)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}, objs...)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}

func newEngine(t *testing.T) *pdfium.Engine {
	t.Helper()
	e, err := pdfium.New(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestInspectExtractRender(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()
	pdf := buildPDF("Hello Yesbook lesson one", "")

	info, err := e.Inspect(ctx, pdf)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(info.Pages))
	}
	if !strings.Contains(info.Pages[0].Text, "Hello Yesbook") || strings.TrimSpace(info.Pages[1].Text) != "" {
		t.Errorf("page text = %q / %q", info.Pages[0].Text, info.Pages[1].Text)
	}

	ex, err := e.Extract(ctx, processing.ExtractRequest{PDF: pdf, Target: document.Target{ID: "p0001", Pages: []int{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Blocks) != 1 || ex.Blocks[0].Page != 1 || !strings.Contains(ex.Blocks[0].Text, "lesson one") {
		t.Errorf("blocks = %+v", ex.Blocks)
	}

	img, err := e.RenderPNG(ctx, pdf, 1, 72)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 612 || cfg.Height != 792 {
		t.Errorf("rendered %dx%d, want 612x792 at 72 DPI", cfg.Width, cfg.Height)
	}
}

func TestInspectRejectsGarbage(t *testing.T) {
	_, err := newEngine(t).Inspect(context.Background(), []byte("%PDF-1.7 this is not really a pdf"))
	if !errors.Is(err, processing.ErrUnreadablePDF) {
		t.Errorf("err = %v, want ErrUnreadablePDF", err)
	}
}
