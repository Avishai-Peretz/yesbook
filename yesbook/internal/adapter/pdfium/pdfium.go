// Package pdfium reads PDFs with PDFium compiled to WebAssembly, so parsing
// untrusted files runs sandboxed and without cgo. It inspects documents,
// extracts the embedded text layer and renders pages for vision models.
package pdfium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"regexp"
	"strings"

	gopdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

// Version pins the go-pdfium release, which bundles its PDFium build.
const Version = "go-pdfium-1.21.4"

type Engine struct {
	pool gopdfium.Pool
}

// New starts a pool of WebAssembly PDFium instances; size bounds how many
// documents are parsed at once.
func New(ctx context.Context, size int) (*Engine, error) {
	pool, err := webassembly.Init(webassembly.Config{Context: ctx, MinIdle: 1, MaxIdle: size, MaxTotal: size})
	if err != nil {
		return nil, fmt.Errorf("start pdfium: %w", err)
	}
	return &Engine{pool: pool}, nil
}

func (e *Engine) Close() error { return e.pool.Close() }

func (e *Engine) Name() string    { return "pdfium" }
func (e *Engine) Version() string { return Version }

// Inspect returns the text layer and image count of every page.
func (e *Engine) Inspect(ctx context.Context, pdf []byte) (processing.PDFInfo, error) {
	var info processing.PDFInfo
	err := e.withDocument(ctx, pdf, func(inst gopdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc})
		if err != nil {
			return fmt.Errorf("count pages: %w", err)
		}
		info.Pages = make([]processing.PageInfo, 0, count.PageCount)
		for i := range count.PageCount {
			if err := ctx.Err(); err != nil {
				return err
			}
			p, err := inspectPage(inst, doc, i)
			if err != nil {
				return fmt.Errorf("page %d: %w", i+1, err)
			}
			info.Pages = append(info.Pages, p)
		}
		return nil
	})
	return info, err
}

func inspectPage(inst gopdfium.Pdfium, doc references.FPDF_DOCUMENT, index int) (processing.PageInfo, error) {
	page, err := inst.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: doc, Index: index})
	if err != nil {
		return processing.PageInfo{}, err
	}
	defer inst.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: page.Page}) //nolint:errcheck // closing a read-only page cannot lose data
	ref := requests.Page{ByReference: &page.Page}

	text, err := inst.GetPageText(&requests.GetPageText{Page: ref})
	if err != nil {
		return processing.PageInfo{}, fmt.Errorf("read text: %w", err)
	}
	objects, err := inst.FPDFPage_CountObjects(&requests.FPDFPage_CountObjects{Page: ref})
	if err != nil {
		return processing.PageInfo{}, fmt.Errorf("count objects: %w", err)
	}
	images := 0
	for i := range objects.Count {
		obj, err := inst.FPDFPage_GetObject(&requests.FPDFPage_GetObject{Page: ref, Index: i})
		if err != nil {
			return processing.PageInfo{}, fmt.Errorf("read object: %w", err)
		}
		typ, err := inst.FPDFPageObj_GetType(&requests.FPDFPageObj_GetType{PageObject: obj.PageObject})
		if err != nil {
			return processing.PageInfo{}, fmt.Errorf("read object type: %w", err)
		}
		if typ.Type == enums.FPDF_PAGEOBJ_IMAGE {
			images++
		}
	}
	return processing.PageInfo{Number: index + 1, Text: text.Text, Images: images}, nil
}

var paragraphBreak = regexp.MustCompile(`(\r?\n){2,}`)

// Extract returns the embedded text of the target's pages, one paragraph
// block per blank-line separated chunk.
func (e *Engine) Extract(ctx context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	var ex document.Extraction
	err := e.withDocument(ctx, req.PDF, func(inst gopdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		for _, n := range req.Target.Pages {
			text, err := inst.GetPageText(&requests.GetPageText{
				Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: n - 1}},
			})
			if err != nil {
				return fmt.Errorf("page %d: %w", n, err)
			}
			for _, para := range paragraphBreak.Split(text.Text, -1) {
				para = strings.TrimSpace(strings.ReplaceAll(para, "\r\n", "\n"))
				if para != "" {
					ex.Blocks = append(ex.Blocks, document.Block{Kind: document.BlockParagraph, Text: para, Page: n})
				}
			}
		}
		return nil
	})
	return ex, err
}

// RenderPNG renders one 1-based page at dpi.
func (e *Engine) RenderPNG(ctx context.Context, pdf []byte, page, dpi int) ([]byte, error) {
	var out []byte
	err := e.withDocument(ctx, pdf, func(inst gopdfium.Pdfium, doc references.FPDF_DOCUMENT) error {
		r, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: page - 1}},
			DPI:  dpi,
		})
		if err != nil {
			return fmt.Errorf("render page %d: %w", page, err)
		}
		defer r.Cleanup()
		var buf bytes.Buffer
		if err := png.Encode(&buf, r.Result.RenderedImage); err != nil {
			return fmt.Errorf("encode page %d: %w", page, err)
		}
		out = buf.Bytes()
		return nil
	})
	return out, err
}

func (e *Engine) withDocument(ctx context.Context, pdf []byte, fn func(gopdfium.Pdfium, references.FPDF_DOCUMENT) error) error {
	inst, err := e.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return fmt.Errorf("get pdfium instance: %w", err)
	}
	defer inst.Close() //nolint:errcheck // returning an instance to the pool has no recoverable failure

	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &pdf})
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword), errors.Is(err, pdfiumerrors.ErrSecurity):
		return fmt.Errorf("%w: %w", processing.ErrEncryptedPDF, err)
	case err != nil:
		return fmt.Errorf("%w: %w", processing.ErrUnreadablePDF, err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document}) //nolint:errcheck // read-only document
	return fn(inst, doc.Document)
}
