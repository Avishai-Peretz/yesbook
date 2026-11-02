package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strconv"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

func (p *Pipeline) commit(
	ctx context.Context,
	log *slog.Logger,
	run progressRun,
	j *document.Job,
	a assembly,
	pages int,
	cacheKey string,
) (document.Status, error) {
	if err := p.record(ctx, run, progressUpdate{stage: StagePublish, status: ProgressStarted}); err != nil {
		return "", err
	}
	now := p.now().UTC()
	versionID := p.newID()
	blocks, err := p.persistAssets(ctx, j.TenantID(), versionID, a.blocks)
	if err != nil {
		return "", p.failStages(ctx, run, err, StagePublish)
	}
	contentRef := storage.Ref{Bucket: storage.BucketDerived, Name: path.Join(j.TenantID(), versionID, "content.json")}
	data, err := json.Marshal(document.Content{
		VersionID:       versionID,
		JobID:           j.ID(),
		Status:          a.outcome,
		Coverage:        a.coverage,
		PipelineVersion: p.version,
		Pages:           pages,
		Blocks:          blocks,
		Review:          a.review,
		Missing:         a.missing,
		CreatedAt:       now,
	})
	if err != nil {
		return "", p.failStages(ctx, run, fmt.Errorf("encode content: %w", err), StagePublish)
	}
	if err := p.objects.Write(ctx, contentRef, "application/json", data); err != nil {
		return "", p.failStages(ctx, run, fmt.Errorf("write content: %w", err), StagePublish)
	}

	if err := j.Complete(a.outcome, versionID, a.reason, now); err != nil {
		return "", p.failStages(ctx, run, err, StagePublish)
	}
	c := Commit{
		Job:   j,
		Event: j.Event(p.newID(), now),
		Version: document.Version{
			ID:              versionID,
			TenantID:        j.TenantID(),
			JobID:           j.ID(),
			FileHash:        j.FileHash(),
			PipelineVersion: p.version,
			Status:          a.outcome,
			Coverage:        a.coverage,
			ContentObject:   contentRef.Name,
			OriginalObject:  j.OriginalObject(),
			CreatedAt:       now,
		},
		Quality: a.quality,
	}
	if a.outcome == document.StatusReady {
		c.CacheKey = cacheKey
	}
	if err := p.versions.CommitVersion(ctx, c); err != nil {
		return "", p.failStages(ctx, run, fmt.Errorf("commit version: %w", err), StagePublish)
	}
	meta := countedProgress(1, 1, progressUnitDocuments)
	if pages > 0 {
		meta = countedProgress(pages, pages, progressUnitPages)
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StagePublish,
		status: ProgressAdvancing,
		meta:   meta,
	}); err != nil {
		return "", err
	}
	if err := p.record(ctx, run, progressUpdate{
		stage:  StagePublish,
		status: ProgressCompleted,
		reason: a.reason,
	}); err != nil {
		return "", err
	}
	log.InfoContext(ctx, "version committed", "version_id", versionID, "status", a.outcome, "coverage", a.coverage)
	return a.outcome, nil
}

// persistAssets writes figure AssetData into the derived bucket and replaces
// it with Asset object refs on the published blocks.
func (p *Pipeline) persistAssets(ctx context.Context, tenantID, versionID string, blocks []document.Block) ([]document.Block, error) {
	out := make([]document.Block, len(blocks))
	copy(out, blocks)
	n := 0
	for i := range out {
		b := &out[i]
		if len(b.AssetData) == 0 {
			b.AssetData = nil
			continue
		}
		n++
		contentType := "application/octet-stream"
		ext := "bin"
		if b.Asset != nil && b.Asset.ContentType != "" {
			contentType = b.Asset.ContentType
		}
		switch contentType {
		case "image/png":
			ext = "png"
		case "image/jpeg":
			ext = "jpg"
		case "image/webp":
			ext = "webp"
		case "image/gif":
			ext = "gif"
		}
		name := path.Join(tenantID, versionID, "assets", strconv.Itoa(n)+"."+ext)
		ref := storage.Ref{Bucket: storage.BucketDerived, Name: name}
		if err := p.objects.Write(ctx, ref, contentType, b.AssetData); err != nil {
			return nil, fmt.Errorf("write asset %s: %w", name, err)
		}
		b.Asset = &document.AssetRef{Object: name, ContentType: contentType}
		b.AssetData = nil
	}
	return out, nil
}
