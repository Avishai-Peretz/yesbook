package document

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Version is an accepted, published extraction of a file.
type Version struct {
	ID              string
	TenantID        string
	JobID           string
	FileHash        string
	PipelineVersion string
	Status          Status
	Coverage        float64
	ContentObject   string
	OriginalObject  string
	CreatedAt       time.Time
}

// Content is the published JSON document of a version.
type Content struct {
	VersionID       string       `json:"version_id"`
	JobID           string       `json:"job_id"`
	Status          Status       `json:"status"`
	Coverage        float64      `json:"coverage"`
	PipelineVersion string       `json:"pipeline_version"`
	Pages           int          `json:"pages"`
	Blocks          []Block      `json:"blocks"`
	Review          []ReviewItem `json:"review,omitempty"`
	Missing         []ReviewItem `json:"missing,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}

// CacheKey identifies the result of processing a file with one pipeline
// configuration, so a hash match only reuses output produced the same way.
func CacheKey(tenantID, fileHash, pipelineVersion string) string {
	sum := sha256.Sum256([]byte(tenantID + "\n" + fileHash + "\n" + pipelineVersion))
	return hex.EncodeToString(sum[:])
}
