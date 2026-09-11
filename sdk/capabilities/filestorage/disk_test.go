// External test package (filestorage_test) so this file can import
// filestoragetest, which itself imports filestorage — an in-package test
// file (package filestorage) importing filestoragetest would be an import
// cycle (see sdk/capabilities/cacher/memory_conformance_test.go for the same pattern).
package filestorage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage/filestoragetest"
)

func newDisk(t *testing.T) filestorage.Storer {
	t.Helper()
	d, err := filestorage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestDisk_Conformance(t *testing.T) {
	filestoragetest.Run(t, newDisk)
}

func TestDiskRejectsSymlinksAndDirectories(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(base, "leaf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"link/secret", "leaf", "directory", ".gopernicus-tmp", ".gopernicus-tmp/private"} {
		t.Run(key, func(t *testing.T) {
			if err := d.Upload(ctx, key, strings.NewReader("overwrite")); !errors.Is(err, filestorage.ErrInvalidPath) {
				t.Errorf("Upload: %v", err)
			}
			if err := d.Delete(ctx, key); !errors.Is(err, filestorage.ErrInvalidPath) {
				t.Errorf("Delete: %v", err)
			}
			if _, err := d.Exists(ctx, key); !errors.Is(err, filestorage.ErrInvalidPath) {
				t.Errorf("Exists: %v", err)
			}
			if _, err := d.GetObjectSize(ctx, key); !errors.Is(err, filestorage.ErrInvalidPath) {
				t.Errorf("Size: %v", err)
			}
			if r, err := d.Download(ctx, key); !errors.Is(err, filestorage.ErrInvalidPath) {
				if r != nil {
					_ = r.Close()
				}
				t.Errorf("Download: %v", err)
			}
		})
	}
	got, err := os.ReadFile(filepath.Join(outside, "secret"))
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside target changed: %q, %v", got, err)
	}
	if err := d.Delete(ctx, ""); !errors.Is(err, filestorage.ErrInvalidPath) {
		t.Fatalf("empty Delete: %v", err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("root removed: %v", err)
	}
}

func TestDiskRefusesUnsafeStagingDirectory(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			base := t.TempDir()
			staging := filepath.Join(base, ".gopernicus-tmp")
			if symlink {
				if err := os.Mkdir(filepath.Join(base, "public"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("public", staging); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(staging, []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
			if d, err := filestorage.NewDisk(base); !errors.Is(err, filestorage.ErrInvalidPath) {
				if d != nil {
					_ = d.Close()
				}
				t.Fatalf("unsafe staging accepted: %v", err)
			}
		})
	}
}

func TestDiskPublishesAtomicallyAcrossInstances(t *testing.T) {
	base := t.TempDir()
	first, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	if err := first.Upload(ctx, "object", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	if err := first.Upload(ctx, ".gopernicus-tmp-other/file", strings.NewReader("neighbor")); err != nil {
		t.Fatal(err)
	}
	old, err := second.Download(ctx, "object")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	result := make(chan error, 1)
	finished := false
	defer func() {
		once.Do(func() { close(release) })
		if !finished {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("upload cleanup did not finish before closing Disk")
			}
		}
	}()
	go func() { result <- first.Upload(ctx, "object", &pausedReader{entered: entered, release: release}) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach source")
	}
	for _, prefix := range []string{"", ".", ".gopernicus-tmp"} {
		got, err := second.List(ctx, prefix)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{".gopernicus-tmp-other/file"}
		if prefix == "" {
			want = append(want, "object")
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("List(%q) exposed staging or lost object: %v", prefix, got)
		}
	}
	current, err := second.Download(ctx, "object")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(current)
	_ = current.Close()
	if err != nil || string(got) != "original" {
		t.Fatalf("in-progress replacement = %q, %v", got, err)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-result:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish")
	}
	got, err = io.ReadAll(old)
	if err != nil || string(got) != "original" {
		t.Fatalf("already open reader changed: %q, %v", got, err)
	}
	current, err = second.Download(ctx, "object")
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(current)
	_ = current.Close()
	if err != nil || string(got) != "replacement" {
		t.Fatalf("published object = %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(base, ".gopernicus-tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v, %v", entries, err)
	}
}

func TestDiskReaderCancellationAndOwnership(t *testing.T) {
	d := newDisk(t).(*filestorage.Disk)
	source := &trackedSource{Reader: strings.NewReader("bytes")}
	if err := d.Upload(context.Background(), "file", source); err != nil {
		t.Fatal(err)
	}
	if source.closed {
		t.Fatal("Upload closed caller source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r, err := d.Download(ctx, "file")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cancel()
	if _, err := io.ReadAll(r); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	live, err := d.Download(context.Background(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(live)
	if err != nil || string(got) != "bytes" {
		t.Fatalf("Disk.Close invalidated caller reader: %q, %v", got, err)
	}
	if _, err := d.Exists(context.Background(), "absent"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed root reported normal absence: %v", err)
	}
}

type pausedReader struct {
	entered chan struct{}
	release <-chan struct{}
	read    bool
}

func (r *pausedReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.entered)
	<-r.release
	return copy(p, "replacement"), io.EOF
}

type trackedSource struct {
	io.Reader
	closed bool
}

func (r *trackedSource) Close() error { r.closed = true; return nil }

func TestDiskDoesNotAdvertiseOptionalProtocols(t *testing.T) {
	d := newDisk(t)
	if _, ok := d.(filestorage.ResumableUploader); ok {
		t.Fatal("Disk advertises resumable uploads")
	}
	if _, ok := d.(filestorage.SignedURLer); ok {
		t.Fatal("Disk advertises signed URLs")
	}
}

func TestDiskSourcePanicLeavesNoPublishedObject(t *testing.T) {
	base := t.TempDir()
	d, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	func() {
		defer func() {
			if recover() != "source panic" {
				t.Error("caller panic was swallowed or changed")
			}
		}()
		_ = d.Upload(context.Background(), "failed", panicReader{})
	}()
	if found, err := d.Exists(context.Background(), "failed"); err != nil || found {
		t.Fatalf("panicked upload visible: %v, %v", found, err)
	}
	entries, err := os.ReadDir(filepath.Join(base, ".gopernicus-tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("panicked upload debris: %v, %v", entries, err)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("source panic") }

func TestDiskStagingReservationIgnoresCase(t *testing.T) {
	base := t.TempDir()
	// On case-insensitive filesystems this is also the actual staging directory.
	if err := os.Mkdir(filepath.Join(base, ".GOPERNICUS-TMP"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".GOPERNICUS-TMP", "unfinished"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	for _, key := range []string{".GOPERNICUS-TMP/unfinished", ".Gopernicus-Tmp/new"} {
		if _, err := d.Exists(ctx, key); !errors.Is(err, filestorage.ErrInvalidPath) {
			t.Errorf("reserved key %q = %v", key, err)
		}
		if err := d.Upload(ctx, key, strings.NewReader("x")); !errors.Is(err, filestorage.ErrInvalidPath) {
			t.Errorf("reserved upload %q = %v", key, err)
		}
	}
	for _, prefix := range []string{"", ".", ".GOPERNICUS-TMP", ".gopernicus-tmp"} {
		got, err := d.List(ctx, prefix)
		if err != nil || len(got) != 0 {
			t.Errorf("List(%q) exposed staging: %v, %v", prefix, got, err)
		}
	}
	if _, err := d.List(ctx, ".GOPERNICUS-TMP/"); !errors.Is(err, filestorage.ErrInvalidPath) {
		t.Errorf("reserved prefix = %v", err)
	}
}
