package media

import (
	"context"
	"io"
)

// BlobStore is the narrow binary-storage surface MediaService needs.
// filestorage.Storer implementations satisfy it directly. It is part of the pocket's public
// surface because a host supplies the concrete blob store via cms.Config.
type BlobStore interface {
	Upload(ctx context.Context, path string, reader io.Reader) error
	Download(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
}
