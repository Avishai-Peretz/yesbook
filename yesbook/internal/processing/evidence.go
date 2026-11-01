package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

const maxEvidenceBytes = 32 << 20

// evidenceStore saves the output of every stage under the job, so a retried
// run resumes from saved output instead of repeating paid extractor calls.
type evidenceStore struct {
	objects ObjectStore
}

func evidenceRef(j *document.Job, parts ...string) storage.Ref {
	name := path.Join(append([]string{j.TenantID(), j.ID()}, parts...)...) + ".json"
	return storage.Ref{Bucket: storage.BucketEvidence, Name: name}
}

func extractionKey(t document.Target, e Extractor) string {
	return t.ID + "/" + e.Name() + "@" + e.Version()
}

func (e evidenceStore) load(ctx context.Context, ref storage.Ref, v any) (bool, error) {
	data, err := e.objects.Read(ctx, ref, maxEvidenceBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read evidence %s: %w", ref, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decode evidence %s: %w", ref, err)
	}
	return true, nil
}

func (e evidenceStore) save(ctx context.Context, ref storage.Ref, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", ref, err)
	}
	if err := e.objects.Write(ctx, ref, "application/json", data); err != nil {
		return fmt.Errorf("write evidence %s: %w", ref, err)
	}
	return nil
}
