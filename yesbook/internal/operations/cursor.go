package operations

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/avi-p/yesbook/internal/document"
)

// maxCursorLength bounds a client-supplied cursor before it is decoded.
const maxCursorLength = 2048

// Cursor is an unsigned position in the newest-first document order.
// It does not grant access: every query still filters on the caller's tenant.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

type cursorPayload struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// Encode returns the opaque cursor for this position.
func (c Cursor) Encode() (string, error) {
	if c.ID == "" || c.CreatedAt.IsZero() {
		return "", fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	payload, err := json.Marshal(cursorPayload{CreatedAt: c.CreatedAt.UTC(), ID: c.ID})
	if err != nil {
		return "", fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// ParseCursor decodes a cursor produced by Encode.
func ParseCursor(raw string) (Cursor, error) {
	if raw == "" || len(raw) > maxCursorLength {
		return Cursor{}, fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	dec := json.NewDecoder(bytes.NewReader(decoded))
	dec.DisallowUnknownFields()
	var payload cursorPayload
	if err := dec.Decode(&payload); err != nil {
		return Cursor{}, fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Cursor{}, fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	if payload.ID == "" || payload.CreatedAt.IsZero() {
		return Cursor{}, fmt.Errorf("%w: cursor is invalid", document.ErrInvalid)
	}
	return Cursor{CreatedAt: payload.CreatedAt.UTC(), ID: payload.ID}, nil
}
