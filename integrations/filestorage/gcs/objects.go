package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

// Upload streams reader without closing it, replacing the object only after a
// successful copy. Source failure/cancellation aborts the child upload before
// closing its writer; a blocked source Reader cannot be forcibly interrupted.
func (s *Store) Upload(ctx context.Context, path string, reader io.Reader) error {
	if err := validateObject(ctx, path); err != nil {
		return err
	}
	if reader == nil {
		return fmt.Errorf("gcs: upload reader is nil: %w", sdk.ErrInvalidInput)
	}
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := s.object(path).NewWriter(uploadCtx)
	source := &contextReader{ctx: uploadCtx, reader: reader}
	_, err := io.Copy(writer, source)
	// io.Copy can prefer a write error when Read returns both data and an
	// error. Retain that source error as well, especially during cancellation.
	err = errors.Join(err, source.err, ctx.Err())
	if err != nil {
		cancel()
		if closeErr := writer.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
			err = errors.Join(err, closeErr)
		}
		return fmt.Errorf("gcs: upload source or write failed: %w", err)
	}
	err = writer.Close()
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fmt.Errorf("gcs: completing upload: %w", mapErr(err))
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
	err    error
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

// Download returns stored bytes, including compressed bytes for gzip-encoded
// objects. The caller owns and must close the returned reader.
func (s *Store) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := validateObject(ctx, path); err != nil {
		return nil, err
	}
	reader, err := s.object(path).ReadCompressed(true).NewReader(ctx)
	if ctx.Err() != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, errors.Join(err, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("gcs: download: %w", mapErr(err))
	}
	return reader, nil
}

// Delete removes an object; an already missing object succeeds.
func (s *Store) Delete(ctx context.Context, path string) error {
	if err := validateObject(ctx, path); err != nil {
		return err
	}
	err := s.object(path).Delete(ctx)
	if ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("gcs: delete: %w", mapErr(err))
	}
	return nil
}

// Exists reports object presence, retaining permission/configuration failures.
func (s *Store) Exists(ctx context.Context, path string) (bool, error) {
	if err := validateObject(ctx, path); err != nil {
		return false, err
	}
	_, err := s.object(path).Attrs(ctx)
	if ctx.Err() != nil {
		return false, errors.Join(err, ctx.Err())
	}
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gcs: checking object existence: %w", mapErr(err))
	}
	return true, nil
}

// List materializes caller-facing keys matching the literal prefix, excluding
// provider directory markers. It makes no ordering guarantee.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := filestorage.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	it := s.client.Bucket(s.bucket).Objects(ctx, &gcsstorage.Query{Prefix: s.key(prefix)})
	var paths []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attrs, err := it.Next()
		if ctx.Err() != nil {
			return nil, errors.Join(err, ctx.Err())
		}
		if errors.Is(err, iterator.Done) {
			return paths, nil
		}
		if err != nil {
			return nil, fmt.Errorf("gcs: listing objects: %w", mapErr(err))
		}
		if !strings.HasSuffix(attrs.Name, "/") {
			path := strings.TrimPrefix(attrs.Name, s.prefix)
			if err := filestorage.ValidatePath(path); err != nil {
				return nil, fmt.Errorf("gcs: listed object has an incompatible key: %w", err)
			}
			paths = append(paths, path)
		}
	}
}

// DownloadRange returns stored bytes and truncates at EOF. Empty ranges confirm
// existence first. A 416 becomes EOF only when a subsequent stat confirms the
// offset is at/beyond EOF; concurrent replacements are not a shared snapshot.
func (s *Store) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	if err := validateObject(ctx, path); err != nil {
		return nil, err
	}
	if err := filestorage.ValidateRange(offset, length); err != nil {
		return nil, err
	}
	if length == 0 {
		if _, err := s.GetObjectSize(ctx, path); err != nil {
			return nil, err
		}
		return io.NopCloser(strings.NewReader("")), nil
	}
	reader, err := s.object(path).ReadCompressed(true).NewRangeReader(ctx, offset, length)
	if ctx.Err() != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, errors.Join(err, ctx.Err())
	}
	if err != nil {
		var response *googleapi.Error
		if errors.As(err, &response) && response.Code == http.StatusRequestedRangeNotSatisfiable {
			size, statErr := s.GetObjectSize(ctx, path)
			if statErr != nil {
				return nil, statErr
			}
			if offset >= size {
				return io.NopCloser(strings.NewReader("")), nil
			}
		}
		return nil, fmt.Errorf("gcs: download range: %w", mapErr(err))
	}
	return reader, nil
}

// GetObjectSize returns the object's stored byte length.
func (s *Store) GetObjectSize(ctx context.Context, path string) (int64, error) {
	if err := validateObject(ctx, path); err != nil {
		return 0, err
	}
	attrs, err := s.object(path).Attrs(ctx)
	if ctx.Err() != nil {
		return 0, errors.Join(err, ctx.Err())
	}
	if err != nil {
		return 0, fmt.Errorf("gcs: reading object size: %w", mapErr(err))
	}
	return attrs.Size, nil
}

func validateObject(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return filestorage.ValidatePath(path)
}

func mapErr(err error) error {
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return errors.Join(filestorage.ErrObjectNotFound, err)
	}
	return err
}
