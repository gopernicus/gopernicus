// Package media is the bounded context for uploaded assets (images, docs). It
// stores asset metadata via an AssetRepository and binary content via a
// BlobStore (sdk/filestorage), owning the CMS rules — storage-key generation
// and basic validation.
package media

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/id"
	"github.com/gopernicus/gopernicus/sdk/slug"
)

// Asset is the media aggregate: metadata for one stored file.
type Asset struct {
	ID          string
	Filename    string // original upload name
	ContentType string
	Size        int64
	StorageKey  string // path within the blob store
	Alt         string // alt text for images
	CreatedAt   time.Time
}

// NewAsset validates inputs, generates an ID and a storage key, and returns a
// new Asset. Validation failures wrap errs.ErrInvalidInput.
func NewAsset(filename, contentType string, size int64, now time.Time) (Asset, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return Asset{}, fmt.Errorf("filename is required: %w", errs.ErrInvalidInput)
	}
	if contentType == "" {
		return Asset{}, fmt.Errorf("content type is required: %w", errs.ErrInvalidInput)
	}
	if size <= 0 {
		return Asset{}, fmt.Errorf("file is empty: %w", errs.ErrInvalidInput)
	}

	assetID := id.New()
	return Asset{
		ID:          assetID,
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		StorageKey:  storageKey(assetID, filename),
		CreatedAt:   now.UTC(),
	}, nil
}

// storageKey builds a collision-free, sanitized path: assets/<id>/<slug><ext>.
func storageKey(assetID, filename string) string {
	ext := path.Ext(filename)
	name := slug.Make(strings.TrimSuffix(filename, ext))
	if name == "" {
		name = "file"
	}
	return "assets/" + assetID + "/" + name + strings.ToLower(ext)
}
