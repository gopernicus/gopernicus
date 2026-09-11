//go:build unix

package filestorage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

func TestDiskRejectsFIFOBeforeOpening(t *testing.T) {
	base := t.TempDir()
	d, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fifo := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		r, err := d.Download(context.Background(), "pipe")
		if r != nil {
			_ = r.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, filestorage.ErrInvalidPath) {
			t.Fatalf("FIFO accepted: %v", err)
		}
	case <-time.After(2 * time.Second):
		// Release a mistakenly blocked read before failing, without hanging cleanup.
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("Download blocked opening FIFO")
	}
}

func TestDiskPublishesPrivateFiles(t *testing.T) {
	base := t.TempDir()
	d, err := filestorage.NewDisk(base)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.WriteFile(filepath.Join(base, "existing"), []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"new", "existing"} {
		if err := d.Upload(context.Background(), key, strings.NewReader("private")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(base, key))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions = %o", key, info.Mode().Perm())
		}
	}
}
