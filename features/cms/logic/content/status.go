package content

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/slug"
)

// Status is an entry's publication state.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	return s == StatusDraft || s == StatusPublished
}

// validate enforces the spine invariants shared by every entry, independent of
// persistence: a non-empty, sluggable title and a valid status.
func validate(title string, status Status) error {
	if title == "" {
		return fmt.Errorf("title is required: %w", errs.ErrInvalidInput)
	}
	if slug.Make(title) == "" {
		return fmt.Errorf("title must contain at least one alphanumeric character: %w", errs.ErrInvalidInput)
	}
	if !status.Valid() {
		return fmt.Errorf("invalid status %q: %w", status, errs.ErrInvalidInput)
	}
	return nil
}
