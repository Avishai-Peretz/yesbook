package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/storage"
)

// DocumentDeleter removes a job and, when it holds the last reference,
// shared version rows and their objects.
type DocumentDeleter interface {
	DeleteDocument(ctx context.Context, tenantID, jobID string) (DeletePlan, error)
}

// DeletePlan lists object-store cleanup that must run after the DB commit.
type DeletePlan struct {
	DeleteRefs     []storage.Ref
	DeletePrefix   []storage.Ref // Bucket+Name used as prefix (Name is prefix)
	DropOriginal   bool
	OriginalObject string
	DropVersion    bool
	VersionID      string
	ContentObject  string
	TenantID       string
}

type ObjectsDeleter interface {
	Delete(ctx context.Context, ref storage.Ref) error
	DeletePrefix(ctx context.Context, bucket storage.Bucket, prefix string) error
	Read(ctx context.Context, ref storage.Ref, maxBytes int64) ([]byte, error)
}

// Delete hard-deletes a tenant-scoped document and garbage-collects shared
// version/original/derived assets when this was the last reference.
func (s *Service) Delete(ctx context.Context, tenantID, jobID string) error {
	if tenantID == "" || jobID == "" {
		return fmt.Errorf("%w: tenant and document are required", document.ErrInvalid)
	}
	deleter, ok := s.jobs.(DocumentDeleter)
	if !ok {
		return fmt.Errorf("delete is not supported by this store")
	}
	plan, err := deleter.DeleteDocument(ctx, tenantID, jobID)
	if err != nil {
		return err
	}
	blobs, ok := s.uploads.(ObjectsDeleter)
	if !ok {
		return nil
	}
	return applyDeletePlan(ctx, blobs, plan)
}

func applyDeletePlan(ctx context.Context, blobs ObjectsDeleter, plan DeletePlan) error {
	var errs []error
	assetRefs := collectAssetRefs(ctx, blobs, plan)
	for _, ref := range plan.DeleteRefs {
		if err := blobs.Delete(ctx, ref); err != nil {
			errs = append(errs, err)
		}
	}
	for _, pref := range plan.DeletePrefix {
		if err := blobs.DeletePrefix(ctx, pref.Bucket, pref.Name); err != nil {
			errs = append(errs, err)
		}
	}
	if plan.DropVersion {
		for _, ref := range assetRefs {
			if err := blobs.Delete(ctx, ref); err != nil {
				errs = append(errs, err)
			}
		}
		if plan.ContentObject != "" {
			if err := blobs.Delete(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: plan.ContentObject}); err != nil {
				errs = append(errs, err)
			}
		}
		if plan.TenantID != "" && plan.VersionID != "" {
			assetPrefix := path.Join(plan.TenantID, plan.VersionID, "assets")
			if err := blobs.DeletePrefix(ctx, storage.BucketDerived, assetPrefix); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if plan.DropOriginal && plan.OriginalObject != "" {
		if err := blobs.Delete(ctx, storage.Ref{Bucket: storage.BucketOriginals, Name: plan.OriginalObject}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func collectAssetRefs(ctx context.Context, blobs ObjectsDeleter, plan DeletePlan) []storage.Ref {
	if !plan.DropVersion || plan.ContentObject == "" {
		return nil
	}
	raw, err := blobs.Read(ctx, storage.Ref{Bucket: storage.BucketDerived, Name: plan.ContentObject}, 64<<20)
	if err != nil {
		return nil
	}
	var content document.Content
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil
	}
	out := make([]storage.Ref, 0)
	for _, b := range content.Blocks {
		if b.Asset == nil || b.Asset.Object == "" {
			continue
		}
		out = append(out, storage.Ref{Bucket: storage.BucketDerived, Name: b.Asset.Object})
	}
	return out
}
