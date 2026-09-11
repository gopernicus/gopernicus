package filestoragetest

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

func testPortableKeys(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	for _, key := range []string{".segovia/boot-probe", "space and café/名字..txt"} {
		if err := s.Upload(ctx, key, strings.NewReader(key)); err != nil {
			t.Fatalf("Upload(%q): %v", key, err)
		}
		assertContent(t, s, key, key)
	}
	for _, key := range []string{"", ".", "..", "/absolute", "../alias", "a/../alias", "a/./b", "a//b", "a/", "a\\b", "a\x00b", "a\xffb"} {
		t.Run(key, func(t *testing.T) {
			reader := &countedReader{reader: strings.NewReader("unexpected")}
			var errs []error
			errs = append(errs, s.Upload(ctx, key, reader), s.Delete(ctx, key))
			_, err := s.Exists(ctx, key)
			errs = append(errs, err)
			_, err = s.GetObjectSize(ctx, key)
			errs = append(errs, err)
			r, err := s.Download(ctx, key)
			if r != nil {
				_ = r.Close()
			}
			errs = append(errs, err)
			r, err = s.DownloadRange(ctx, key, 0, -1)
			if r != nil {
				_ = r.Close()
			}
			errs = append(errs, err)
			for i, err := range errs {
				if !errors.Is(err, filestorage.ErrInvalidPath) || !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("invalid-key operation %d = %v, want path + root invalid input", i, err)
				}
			}
			if reader.reads != 0 {
				t.Errorf("invalid Upload read source %d times", reader.reads)
			}
		})
	}
}

func testLiteralPrefixes(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	keys := []string{"img/a.txt", "image.txt", "images/b.txt", "other.txt", ".segovia/boot-probe", "two..dots"}
	for _, key := range keys {
		if err := s.Upload(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, prefix := range []string{"", "im", "img/", "image", ".", "two..", "no-match"} {
		got, err := s.List(ctx, prefix)
		if err != nil {
			t.Fatalf("List(%q): %v", prefix, err)
		}
		var want []string
		for _, key := range keys {
			if strings.HasPrefix(key, prefix) {
				want = append(want, key)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("List(%q) = %v, want %v", prefix, got, want)
		}
	}
	for _, prefix := range []string{"/", "a//", "../", "a/../", "a/./", "a\\b", "a\x00b"} {
		if _, err := s.List(ctx, prefix); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("List invalid prefix %q = %v", prefix, err)
		}
	}
}

func testStreamingReplacement(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	// The wrapper exposes only Reader: neither seeking nor WriterTo is available.
	if err := s.Upload(ctx, "stream", struct{ io.Reader }{strings.NewReader("original")}); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("source rejected upload")
	for _, key := range []string{"stream", "failed-new"} {
		if err := s.Upload(ctx, key, &failedReader{cause: cause}); !errors.Is(err, cause) {
			t.Fatalf("Upload failed source = %v, want source error", err)
		}
	}
	assertContent(t, s, "stream", "original")
	if exists, err := s.Exists(ctx, "failed-new"); err != nil || exists {
		t.Fatalf("failed new upload visible: %v, %v", exists, err)
	}
	if err := s.Upload(ctx, "stream", strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	assertContent(t, s, "stream", "replacement")
}

func testCancellation(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	if err := s.Upload(ctx, "keep", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	reader := &countedReader{reader: strings.NewReader("unexpected")}
	var errs []error
	errs = append(errs, s.Upload(canceled, "new", reader), s.Delete(canceled, "keep"))
	_, err := s.Exists(canceled, "keep")
	errs = append(errs, err)
	_, err = s.List(canceled, "")
	errs = append(errs, err)
	_, err = s.GetObjectSize(canceled, "keep")
	errs = append(errs, err)
	r, err := s.Download(canceled, "keep")
	if r != nil {
		_ = r.Close()
	}
	errs = append(errs, err)
	r, err = s.DownloadRange(canceled, "keep", 0, -1)
	if r != nil {
		_ = r.Close()
	}
	errs = append(errs, err)
	for i, err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled operation %d: %v", i, err)
		}
	}
	if reader.reads != 0 {
		t.Errorf("canceled Upload read source %d times", reader.reads)
	}
	if exists, err := s.Exists(ctx, "new"); err != nil || exists {
		t.Errorf("canceled upload visible: %v, %v", exists, err)
	}
	assertContent(t, s, "keep", "original")

	during, stop := context.WithCancel(ctx)
	defer stop()
	if err := s.Upload(during, "keep", &cancelingReader{cancel: stop}); !errors.Is(err, context.Canceled) {
		t.Fatalf("source canceled upload: %v", err)
	}
	assertContent(t, s, "keep", "original")
	failed, failCancel := context.WithCancel(ctx)
	defer failCancel()
	cause := errors.New("source failure accompanying cancellation")
	err = s.Upload(failed, "keep", &cancelingFailedReader{cancel: failCancel, cause: cause})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("concurrent source failure/cancellation lost a cause: %v", err)
	}
	assertContent(t, s, "keep", "original")

}

func testRangeEdges(t *testing.T, s filestorage.Storer) {
	ctx := context.Background()
	for key, value := range map[string]string{"range": "0123456789", "empty": ""} {
		if err := s.Upload(ctx, key, strings.NewReader(value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		key            string
		offset, length int64
		want           string
	}{
		{"range", 0, 0, ""}, {"range", 3, 0, ""}, {"range", 9, 5, "9"},
		{"range", 0, 100, "0123456789"}, {"range", 10, -1, ""}, {"range", 10, 1, ""},
		{"range", 100, -1, ""}, {"range", 100, 4, ""}, {"range", math.MaxInt64, 1, ""},
		{"empty", 0, 0, ""}, {"empty", 0, -1, ""}, {"empty", 0, 1, ""}, {"empty", 1, -1, ""},
	} {
		r, err := s.DownloadRange(ctx, tc.key, tc.offset, tc.length)
		if err != nil {
			t.Errorf("range %v: %v", tc, err)
			continue
		}
		got, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil || string(got) != tc.want {
			t.Errorf("range %v = %q, read %v, close %v", tc, got, readErr, closeErr)
		}
	}
	for _, tc := range [][2]int64{{-1, -1}, {0, -2}, {math.MaxInt64, 2}} {
		r, err := s.DownloadRange(ctx, "range", tc[0], tc[1])
		if r != nil {
			_ = r.Close()
		}
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("invalid range %v: %v", tc, err)
		}
	}
	for _, length := range []int64{-1, 0, 1} {
		r, err := s.DownloadRange(ctx, "absent", 0, length)
		if r != nil {
			_ = r.Close()
		}
		if !errors.Is(err, filestorage.ErrObjectNotFound) || !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("missing range length %d: %v", length, err)
		}
	}
	_, err := s.Download(ctx, "absent")
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("missing download: %v", err)
	}
	_, err = s.GetObjectSize(ctx, "absent")
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("missing size: %v", err)
	}
}

func assertContent(t *testing.T, s filestorage.Storer, key, want string) {
	t.Helper()
	r, err := s.Download(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil || string(got) != want {
		t.Fatalf("Download(%q) = %q, read %v, close %v; want %q", key, got, readErr, closeErr, want)
	}
}

type countedReader struct {
	reader io.Reader
	reads  int
}

func (r *countedReader) Read(p []byte) (int, error) { r.reads++; return r.reader.Read(p) }

type failedReader struct{ cause error }

func (r *failedReader) Read(p []byte) (int, error) { return copy(p, "partial"), r.cause }

type cancelingReader struct{ cancel context.CancelFunc }

func (r *cancelingReader) Read(p []byte) (int, error) { r.cancel(); return copy(p, "partial"), io.EOF }

type cancelingFailedReader struct {
	cancel context.CancelFunc
	cause  error
}

func (r *cancelingFailedReader) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, "partial"), r.cause
}
