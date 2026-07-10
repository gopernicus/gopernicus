// Package mediasvc holds the media use-case service, kept internal so it is not
// part of the feature's public SemVer surface (plan §5/B3). The public domain
// types, AssetRepository, and BlobStore port stay in package media.
package mediasvc

import (
	"context"
	"io"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Clock returns the current time. Injected so tests can pin timestamps.
type Clock func() time.Time

// MediaService implements the media use cases over asset metadata + a blob
// store. It owns the CMS rules (key generation, validation via NewAsset).
type MediaService struct {
	assets media.AssetRepository
	blobs  media.BlobStore
	// ids is the app-chosen entity-ID strategy (cms.Config.IDs); zero value →
	// default nanoids.
	ids   cryptids.IDGenerator
	clock Clock
}

// NewService constructs a MediaService. A nil clock defaults to time.Now. ids is
// the app's entity-ID strategy (cms.Config.IDs).
func NewService(assets media.AssetRepository, blobs media.BlobStore, ids cryptids.IDGenerator, clock Clock) *MediaService {
	if clock == nil {
		clock = time.Now
	}
	return &MediaService{assets: assets, blobs: blobs, ids: ids, clock: clock}
}

// Upload validates, stores the bytes, and persists the asset metadata.
func (s *MediaService) Upload(ctx context.Context, filename, contentType string, size int64, reader io.Reader) (media.Asset, error) {
	a, err := media.NewAsset(s.ids, filename, contentType, size, s.clock())
	if err != nil {
		return media.Asset{}, err
	}
	if err := s.blobs.Upload(ctx, a.StorageKey, reader); err != nil {
		return media.Asset{}, err
	}
	return s.assets.Create(ctx, a)
}

// GetAsset returns the asset metadata with the given id.
func (s *MediaService) GetAsset(ctx context.Context, id string) (media.Asset, error) {
	return s.assets.Get(ctx, id)
}

// ListAssets returns all assets, newest first.
func (s *MediaService) ListAssets(ctx context.Context) ([]media.Asset, error) {
	return s.assets.List(ctx)
}

// OpenAsset returns the asset metadata and an open reader for its bytes. The
// caller must close the reader.
func (s *MediaService) OpenAsset(ctx context.Context, id string) (media.Asset, io.ReadCloser, error) {
	a, err := s.assets.Get(ctx, id)
	if err != nil {
		return media.Asset{}, nil, err
	}
	rc, err := s.blobs.Download(ctx, a.StorageKey)
	if err != nil {
		return media.Asset{}, nil, err
	}
	return a, rc, nil
}

// DeleteAsset removes both the bytes and the metadata.
func (s *MediaService) DeleteAsset(ctx context.Context, id string) error {
	a, err := s.assets.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.blobs.Delete(ctx, a.StorageKey); err != nil {
		return err
	}
	return s.assets.Delete(ctx, id)
}
