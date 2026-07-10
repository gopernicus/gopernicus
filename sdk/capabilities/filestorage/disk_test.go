// External test package (filestorage_test) so this file can import
// filestoragetest, which itself imports filestorage — an in-package test
// file (package filestorage) importing filestoragetest would be an import
// cycle (see sdk/capabilities/cacher/memory_conformance_test.go for the same pattern).
package filestorage_test

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage/filestoragetest"
)

func newDisk(t *testing.T) filestorage.Storer {
	t.Helper()
	d, err := filestorage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	return d
}

func TestDisk_Conformance(t *testing.T) {
	filestoragetest.Run(t, newDisk)
}

func TestDisk_OptionalCapabilitiesAbsent(t *testing.T) {
	filestoragetest.RunOptionalCapabilityAbsent(t, newDisk)
}

// TestDisk_ContainsPathTraversal documents Disk's traversal defense: full()
// resolves the given path against "/"+path first (filepath.Clean strips
// leading ".." against that synthetic root), then joins onto base — so a
// "../../etc/passwd" request resolves to base/etc/passwd, not an escape.
// This means a traversal attempt is contained (Exists reports false, no
// error) rather than rejected with filestorage.ErrInvalidPath; the guard
// clause in full() that would return ErrInvalidPath is a defense-in-depth
// backstop that this construction does not appear to reach for ordinary
// POSIX-style paths.
func TestDisk_ContainsPathTraversal(t *testing.T) {
	d, err := filestorage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatalf("NewDisk() error = %v", err)
	}
	ok, err := d.Exists(context.Background(), "../../etc/passwd")
	if err != nil {
		t.Fatalf("Exists(traversal path) error = %v, want nil", err)
	}
	if ok {
		t.Error("Exists(traversal path) = true, want false (must not escape the store's base dir)")
	}
}
