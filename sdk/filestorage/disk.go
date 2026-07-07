package filestorage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Disk is the default Storer over the local filesystem that ships with sdk — no
// external dependency, useful for development and single-node deployments.
// (Cloud filestores like gcs/s3 are separate integration modules.)
type Disk struct {
	base string
}

var _ Storer = (*Disk)(nil)

// NewDisk returns a disk Storer rooted at base, creating the directory if needed.
func NewDisk(base string) (*Disk, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	return &Disk{base: abs}, nil
}

// full resolves a storage path to an absolute file path within base, rejecting
// traversal outside base.
func (s *Disk) full(p string) (string, error) {
	clean := filepath.Clean("/" + p) // root + clean strips any ".."
	full := filepath.Join(s.base, clean)
	if full != s.base && !strings.HasPrefix(full, s.base+string(os.PathSeparator)) {
		return "", ErrInvalidPath
	}
	return full, nil
}

// Upload writes data from reader to path, creating parent directories.
func (s *Disk) Upload(ctx context.Context, path string, reader io.Reader) error {
	full, err := s.full(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.Create(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, reader)
	return err
}

// Download opens the object at path. Missing objects map to ErrObjectNotFound.
func (s *Disk) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	full, err := s.full(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	return f, nil
}

// Delete removes the object at path. A missing object is not an error.
func (s *Disk) Delete(ctx context.Context, path string) error {
	full, err := s.full(path)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Exists reports whether an object exists at path.
func (s *Disk) Exists(ctx context.Context, path string) (bool, error) {
	full, err := s.full(path)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// List returns the storage paths of files under prefix.
func (s *Disk) List(ctx context.Context, prefix string) ([]string, error) {
	root, err := s.full(prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // empty prefix
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.base, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DownloadRange reads length bytes from offset (length -1 = to end).
func (s *Disk) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	full, err := s.full(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	if length < 0 {
		return f, nil
	}
	return &limitedFile{Reader: io.LimitReader(f, length), closer: f}, nil
}

// GetObjectSize returns the byte size of the object at path.
func (s *Disk) GetObjectSize(ctx context.Context, path string) (int64, error) {
	full, err := s.full(path)
	if err != nil {
		return 0, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrObjectNotFound
		}
		return 0, err
	}
	return fi.Size(), nil
}

// limitedFile pairs a limited reader with the underlying file's Close.
type limitedFile struct {
	io.Reader
	closer io.Closer
}

func (l *limitedFile) Close() error { return l.closer.Close() }
