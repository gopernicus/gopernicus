package gcs

import (
	"errors"
	"fmt"
	"testing"

	gcsstorage "cloud.google.com/go/storage"

	"github.com/gopernicus/gopernicus/sdk/filestorage"
)

func TestMapErr(t *testing.T) {
	if got := mapErr(nil); got != nil {
		t.Errorf("mapErr(nil) = %v, want nil", got)
	}

	if got := mapErr(gcsstorage.ErrObjectNotExist); !errors.Is(got, filestorage.ErrObjectNotFound) {
		t.Errorf("mapErr(ErrObjectNotExist) = %v, want errors.Is(_, filestorage.ErrObjectNotFound)", got)
	}

	// Wrapped not-found still maps through errors.Is.
	wrapped := fmt.Errorf("driver: %w", gcsstorage.ErrObjectNotExist)
	if got := mapErr(wrapped); !errors.Is(got, filestorage.ErrObjectNotFound) {
		t.Errorf("mapErr(wrapped not-exist) = %v, want errors.Is(_, filestorage.ErrObjectNotFound)", got)
	}

	// Unrecognized errors pass through unchanged.
	other := errors.New("boom")
	if got := mapErr(other); !errors.Is(got, other) {
		t.Errorf("mapErr(other) = %v, want the same error", got)
	}
	if errors.Is(mapErr(other), filestorage.ErrObjectNotFound) {
		t.Error("mapErr(other) must not map to ErrObjectNotFound")
	}
}

func TestNormalizePrefix(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"/":        "",
		"tenant":   "tenant/",
		"tenant/":  "tenant/",
		"/tenant":  "tenant/",
		"/tenant/": "tenant/",
		"a/b":      "a/b/",
		"a/b/":     "a/b/",
	}
	for in, want := range cases {
		if got := normalizePrefix(in); got != want {
			t.Errorf("normalizePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKey(t *testing.T) {
	cases := []struct {
		prefix string
		path   string
		want   string
	}{
		{"", "dir/file.txt", "dir/file.txt"},
		{"", "/dir/file.txt", "dir/file.txt"},
		{"tenant/", "dir/file.txt", "tenant/dir/file.txt"},
		{"tenant/", "/dir/file.txt", "tenant/dir/file.txt"},
	}
	for _, c := range cases {
		s := &Store{prefix: c.prefix}
		if got := s.key(c.path); got != c.want {
			t.Errorf("Store{prefix:%q}.key(%q) = %q, want %q", c.prefix, c.path, got, c.want)
		}
	}
}

func TestIsDirectory(t *testing.T) {
	if !isDirectory("dir/") {
		t.Error("isDirectory(dir/) = false, want true")
	}
	if isDirectory("dir/file.txt") {
		t.Error("isDirectory(dir/file.txt) = true, want false")
	}
	if isDirectory("") {
		t.Error("isDirectory(empty) = true, want false")
	}
}
