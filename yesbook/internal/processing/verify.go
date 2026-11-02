package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

type verification struct {
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
	Pages     int    `json:"pages"`
}

// verify checks file type, size and PDF structure, computes the file hash
// and stores the verified original. A non-empty reason means the file is
// unusable and the job must be blocked.
func (p *Pipeline) verify(ctx context.Context, run progressRun, j *document.Job) ([]byte, PDFInfo, string, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StageVerify, status: ProgressStarted}); err != nil {
		return nil, PDFInfo{}, "", err
	}
	pdf, err := p.objects.Read(ctx, storage.Ref{Bucket: storage.BucketStaging, Name: j.StagingObject()}, document.MaxSizeBytes)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return p.failVerification(ctx, run, "uploaded file is missing")
	case errors.Is(err, storage.ErrTooLarge):
		return p.failVerification(ctx, run, "file exceeds the size limit")
	case err != nil:
		return nil, PDFInfo{}, "", p.failStages(ctx, run, fmt.Errorf("read upload: %w", err), StageVerify)
	}
	if int64(len(pdf)) != j.SizeBytes() {
		return p.failVerification(ctx, run, fmt.Sprintf("file is %d bytes, %d were declared", len(pdf), j.SizeBytes()))
	}
	if !bytes.Contains(pdf[:min(len(pdf), 1024)], []byte("%PDF-")) {
		return p.failVerification(ctx, run, "file is not a PDF")
	}

	info, err := p.inspector.Inspect(ctx, pdf)
	switch {
	case errors.Is(err, ErrEncryptedPDF):
		return p.failVerification(ctx, run, "PDF is password protected")
	case errors.Is(err, ErrUnreadablePDF):
		return p.failVerification(ctx, run, "PDF structure is invalid")
	case err != nil:
		return nil, PDFInfo{}, "", p.failStages(ctx, run, fmt.Errorf("inspect PDF: %w", err), StageVerify)
	}
	if len(info.Pages) == 0 {
		return p.failVerification(ctx, run, "PDF has no pages")
	}
	if len(info.Pages) > p.cfg.MaxPages {
		return p.failVerification(ctx, run, fmt.Sprintf("PDF has %d pages, the limit is %d", len(info.Pages), p.cfg.MaxPages))
	}

	sum := sha256.Sum256(pdf)
	if err := j.RecordHash(hex.EncodeToString(sum[:]), p.now().UTC()); err != nil {
		return nil, PDFInfo{}, "", p.failStages(ctx, run, err, StageVerify)
	}
	original := storage.Ref{Bucket: storage.BucketOriginals, Name: j.OriginalObject()}
	if err := p.objects.Write(ctx, original, document.ContentTypePDF, pdf); err != nil {
		return nil, PDFInfo{}, "", p.failStages(ctx, run, fmt.Errorf("store verified original: %w", err), StageVerify)
	}
	v := verification{Hash: j.FileHash(), SizeBytes: j.SizeBytes(), Pages: len(info.Pages)}
	if err := p.evidence.save(ctx, evidenceRef(j, "verify"), v); err != nil {
		return nil, PDFInfo{}, "", p.failStages(ctx, run, err, StageVerify)
	}
	pages := len(info.Pages)
	if err := p.record(ctx, run, progressUpdate{
		stage:  StageVerify,
		status: ProgressAdvancing,
		meta:   countedProgress(pages, pages, progressUnitPages),
	}); err != nil {
		return nil, PDFInfo{}, "", p.failStages(ctx, run, err, StageVerify)
	}
	if err := p.record(ctx, run, progressUpdate{stage: StageVerify, status: ProgressCompleted}); err != nil {
		return nil, PDFInfo{}, "", p.failStages(ctx, run, err, StageVerify)
	}
	return pdf, info, "", nil
}

// failVerification records a terminal verification failure that blocks the job.
func (p *Pipeline) failVerification(ctx context.Context, run progressRun, reason string) ([]byte, PDFInfo, string, error) {
	if err := p.record(ctx, run, progressUpdate{
		stage:  StageVerify,
		status: ProgressFailed,
		reason: reason,
	}); err != nil {
		return nil, PDFInfo{}, "", err
	}
	return nil, PDFInfo{}, reason, nil
}

// prepare splits the PDF into page targets with their text layer and
// language, reusing saved targets on a retried run.
func (p *Pipeline) prepare(ctx context.Context, run progressRun, j *document.Job, info PDFInfo) ([]document.Target, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StagePrepare, status: ProgressStarted}); err != nil {
		return nil, err
	}
	ref := evidenceRef(j, "prepare")
	var targets []document.Target
	ok, err := p.evidence.load(ctx, ref, &targets)
	if err != nil {
		return nil, p.failStages(ctx, run, err, StagePrepare)
	}
	if !ok {
		targets = buildTargets(info)
		if err := p.evidence.save(ctx, ref, targets); err != nil {
			return nil, p.failStages(ctx, run, err, StagePrepare)
		}
	}
	if len(targets) > 0 {
		if err := p.record(ctx, run, progressUpdate{
			stage:  StagePrepare,
			status: ProgressAdvancing,
			meta:   countedProgress(len(targets), len(targets), progressUnitTargets),
		}); err != nil {
			return nil, p.failStages(ctx, run, err, StagePrepare)
		}
	}
	if err := p.record(ctx, run, progressUpdate{stage: StagePrepare, status: ProgressCompleted}); err != nil {
		return nil, p.failStages(ctx, run, err, StagePrepare)
	}
	return targets, nil
}

// buildTargets classifies each page for routing. Image-only pages become
// ContentScan (Docling/OCR first); ContentDiagram is reserved for a future
// signal that prefers vision-first routing. Embedded image counts are kept
// on every target so validation can escalate text extractors that drop figures.
func buildTargets(info PDFInfo) []document.Target {
	targets := make([]document.Target, 0, len(info.Pages))
	for _, pg := range info.Pages {
		chars := countChars(pg.Text)
		content := document.ContentScan
		switch {
		case chars >= minTextLayerChars:
			content = document.ContentText
		case chars == 0 && pg.Images == 0:
			content = document.ContentBlank
		}
		t := document.Target{
			ID:             fmt.Sprintf("p%04d", pg.Number),
			Kind:           document.TargetPage,
			Pages:          []int{pg.Number},
			Content:        content,
			Language:       detectLanguage(pg.Text),
			EmbeddedImages: pg.Images,
		}
		if content == document.ContentText {
			t.TextLayer = pg.Text
			t.TextLayerChars = chars
		}
		targets = append(targets, t)
	}
	return targets
}
