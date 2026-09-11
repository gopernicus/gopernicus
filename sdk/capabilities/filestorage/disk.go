package filestorage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

const diskStagingDirectory = ".gopernicus-tmp"

// Disk stores objects as regular files under an opened filesystem root. Construct
// it with NewDisk and close it after its users stop. The zero value is unusable.
//
// Disk reserves the top-level .gopernicus-tmp directory (case-insensitively) for unpublished uploads.
// It rejects symlink components and other non-regular objects. os.Root confines
// filesystem operations even during path changes, but the host must still own and
// trust the tree: hard links, mount/device changes and malicious mutations within
// the root are outside this guarantee. Filesystem case and directory conflicts
// remain platform limits. Uploads crossing a filesystem boundary fail at rename.
// Published files are owner-readable/writable (0600), including replacements.
type Disk struct {
	root *os.Root
}

var _ Storer = (*Disk)(nil)

// NewDisk creates base if necessary, opens it and checks the private staging
// directory. It never removes existing staged files: another instance may own them.
func NewDisk(base string) (*Disk, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("disk: create root: %w", err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("disk: open root: %w", err)
	}
	s := &Disk{root: root}
	if err := s.prepareStaging(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the root handle. It does not drain uploads or close previously
// returned readers. Hosts must stop users first; a racing Close can prevent
// temporary-file cleanup. Crashes can likewise leave hidden staging files.
func (s *Disk) Close() error { return s.root.Close() }

func (s *Disk) prepareStaging() error {
	if err := s.root.Mkdir(diskStagingDirectory, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("disk: create staging directory: %w", err)
	}
	info, err := s.root.Lstat(diskStagingDirectory)
	if err != nil {
		return fmt.Errorf("disk: inspect staging directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("disk: staging path must be a directory, without symlinks: %w", ErrInvalidPath)
	}
	return nil
}

func validateDiskPath(key string) error {
	if err := ValidatePath(key); err != nil {
		return err
	}
	first, _, _ := strings.Cut(key, "/")
	if strings.EqualFold(first, diskStagingDirectory) {
		return fmt.Errorf("disk: reserved staging path: %w", ErrInvalidPath)
	}
	return nil
}

// objectInfo checks every component before an open, so preexisting symlinks,
// directories and special files are never accepted as objects. os.Root, rather
// than these preliminary checks, provides confinement against path races.
func (s *Disk) objectInfo(key string) (fs.FileInfo, error) {
	parts := strings.Split(key, "/")
	var info fs.FileInfo
	for i := range parts {
		var err error
		info, err = s.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("disk: symlink component: %w", ErrInvalidPath)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, fmt.Errorf("disk: parent is not a directory: %w", ErrInvalidPath)
		}
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("disk: object is not a regular file: %w", ErrInvalidPath)
	}
	return info, nil
}

func (s *Disk) openObject(ctx context.Context, key string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDiskPath(key); err != nil {
		return nil, err
	}
	if _, err := s.objectInfo(key); err != nil {
		return nil, diskObjectError(err)
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, diskObjectError(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidPath
	}
	return f, nil
}

func diskObjectError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
	}
	return err
}

// Upload stages data before replacing the destination. A failed copy, close or
// pre-publication cancellation preserves the old object. Rename publishes one
// complete file; it does not promise fsync durability or cross-filesystem copies.
func (s *Disk) Upload(ctx context.Context, key string, reader io.Reader) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDiskPath(key); err != nil {
		return err
	}
	if reader == nil {
		return fmt.Errorf("disk: nil upload reader: %w", sdk.ErrInvalidInput)
	}
	if _, err := s.objectInfo(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := s.root.MkdirAll(path.Dir(key), 0o755); err != nil {
		return fmt.Errorf("disk: create object directory: %w", err)
	}
	// Recheck now that every parent exists, including formerly missing paths.
	if _, err := s.objectInfo(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := s.prepareStaging(); err != nil {
		return err
	}
	staged := diskStagingDirectory + "/" + rand.Text()
	f, err := s.root.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("disk: create staged upload: %w", err)
	}
	defer func() {
		if staged == "" {
			return
		}
		if cleanupErr := s.root.Remove(staged); cleanupErr != nil && !errors.Is(cleanupErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("disk: remove staged upload: %w", cleanupErr))
		}
	}()
	defer f.Close() // Close before removing staging, including on a Reader panic.
	source := &contextReader{ctx: ctx, reader: reader}
	_, copyErr := io.Copy(f, source)
	closeErr := f.Close()
	if err := errors.Join(copyErr, source.err, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("disk: upload %q: %w", key, err)
	}
	if err := s.root.Rename(staged, key); err != nil {
		return fmt.Errorf("disk: publish %q: %w", key, err)
	}
	staged = "" // The completed object no longer needs staging cleanup.
	return nil
}

// Download opens a regular object. The caller owns the returned reader.
func (s *Disk) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	f, err := s.openObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("disk: download %q: %w", key, err)
	}
	return &diskReader{contextReader{ctx: ctx, reader: f}, f}, nil
}

// Delete removes a regular object; an absent object is already deleted.
func (s *Disk) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDiskPath(key); err != nil {
		return err
	}
	if _, err := s.objectInfo(key); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("disk: delete %q: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("disk: delete %q: %w", key, err)
	}
	return nil
}

// Exists reports whether a regular object exists at key.
func (s *Disk) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateDiskPath(key); err != nil {
		return false, err
	}
	if _, err := s.objectInfo(key); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("disk: exists %q: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// List walks regular files matching a literal prefix, omitting private staging
// and symlinks. Concurrent writes/deletes do not provide a listing snapshot.
func (s *Disk) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	first, _, hasSlash := strings.Cut(prefix, "/")
	if hasSlash && strings.EqualFold(first, diskStagingDirectory) {
		return nil, ErrInvalidPath
	}
	var out []string
	err := fs.WalkDir(s.root.FS(), ".", func(key string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if key != "." && errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if strings.EqualFold(key, diskStagingDirectory) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if key != "." && !strings.HasPrefix(prefix, key+"/") && !strings.HasPrefix(key, prefix) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasPrefix(key, prefix) {
			return nil
		}
		if err := ValidatePath(key); err != nil {
			return fmt.Errorf("disk: listed key %q: %w", key, err)
		}
		out = append(out, key)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("disk: list %q: %w", prefix, err)
	}
	return out, nil
}

// DownloadRange follows the shared stored-byte/EOF contract.
func (s *Disk) DownloadRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateRange(offset, length); err != nil {
		return nil, err
	}
	f, err := s.openObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("disk: download range %q: %w", key, err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	var reader io.Reader = f
	if length >= 0 {
		reader = io.LimitReader(f, length)
	}
	return &diskReader{contextReader{ctx: ctx, reader: reader}, f}, nil
}

// GetObjectSize returns the byte size of a regular object.
func (s *Disk) GetObjectSize(ctx context.Context, key string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateDiskPath(key); err != nil {
		return 0, err
	}
	info, err := s.objectInfo(key)
	if err != nil {
		return 0, fmt.Errorf("disk: object size %q: %w", key, diskObjectError(err))
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
	err    error // Preserve the source error if io.Copy instead returns a write error.
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, errors.Join(err, ctxErr)
	}
	return n, err
}

type diskReader struct {
	contextReader
	closer io.Closer
}

func (r *diskReader) Close() error { return r.closer.Close() }
