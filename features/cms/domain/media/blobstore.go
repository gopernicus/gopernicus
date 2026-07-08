package media

import (
	"context"
	"io"
)

// BlobStore is the narrow binary-storage surface MediaService needs.
// *filestorage.FileStore satisfies it. It is part of the feature's public
// surface because a host supplies the concrete blob store via cms.Config.
type BlobStore interface {
	Upload(ctx context.Context, path string, reader io.Reader) error
	Download(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
}
