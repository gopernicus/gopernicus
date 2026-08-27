// Package media is the bounded context for uploaded assets (images, docs). It
// stores asset metadata via an AssetRepository and binary content via a
// BlobStore (sdk/capabilities/filestorage), owning the CMS rules — storage-key generation
// and basic validation.
package media

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/slug"
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

// storageKeys mints the random path component of a StorageKey with the default
// nanoid shape. Deliberately NOT the app's entity-ID strategy: a blob needs its
// collision-free path even when cryptids.Database leaves the entity ID empty
// for the store to assign at insert.
var storageKeys = cryptids.IDGenerator{}

// NewAsset validates inputs, generates a storage key, mints its ID from ids
// (empty under cryptids.Database — the store then assigns the key), and returns a
// new Asset. The storage key carries its own random component, independent of
// the entity ID, so it exists under every ID strategy. Validation failures wrap
// sdk.ErrInvalidInput.
func NewAsset(ids cryptids.IDGenerator, filename, contentType string, size int64, now time.Time) (Asset, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return Asset{}, fmt.Errorf("filename is required: %w", sdk.ErrInvalidInput)
	}
	if contentType == "" {
		return Asset{}, fmt.Errorf("content type is required: %w", sdk.ErrInvalidInput)
	}
	if size <= 0 {
		return Asset{}, fmt.Errorf("file is empty: %w", sdk.ErrInvalidInput)
	}

	return Asset{
		ID:          ids.MustGenerate(),
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		StorageKey:  storageKey(storageKeys.MustGenerate(), filename),
		CreatedAt:   now.UTC(),
	}, nil
}

// storageKey builds a collision-free, sanitized path: assets/<key>/<slug><ext>.
func storageKey(key, filename string) string {
	ext := path.Ext(filename)
	name := slug.Make(strings.TrimSuffix(filename, ext))
	if name == "" {
		name = "file"
	}
	return "assets/" + key + "/" + name + strings.ToLower(ext)
}
