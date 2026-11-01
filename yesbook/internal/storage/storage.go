// Package storage defines the object storage vocabulary shared by use cases
// and storage adapters: the four buckets, object references and errors.
package storage

import (
	"errors"
	"time"
)

// Bucket is a logical bucket. Adapters map each one to a private Cloud
// Storage bucket or a local directory.
type Bucket string

const (
	BucketStaging   Bucket = "staging"
	BucketOriginals Bucket = "originals"
	BucketEvidence  Bucket = "evidence"
	BucketDerived   Bucket = "derived"
)

// Buckets lists every logical bucket.
var Buckets = []Bucket{BucketStaging, BucketOriginals, BucketEvidence, BucketDerived}

var (
	ErrNotFound      = errors.New("object not found")
	ErrTooLarge      = errors.New("object exceeds size limit")
	ErrInvalidObject = errors.New("invalid object reference")
)

// Ref names one object.
type Ref struct {
	Bucket Bucket
	Name   string
}

func (r Ref) String() string {
	return string(r.Bucket) + "/" + r.Name
}

type ObjectInfo struct {
	Size int64
}

// SignedURL is a time-limited URL. The client must send Headers with the request.
type SignedURL struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}
