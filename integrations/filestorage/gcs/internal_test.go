package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}

func testStore(t *testing.T, cfg Config, fn roundTripFunc) *Store {
	t.Helper()
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	if cfg.Bucket == "" {
		cfg.Bucket = "test-bucket"
	}
	st, err := Open(context.Background(), cfg, WithClientOption(option.WithoutAuthentication(), option.WithHTTPClient(&http.Client{Transport: fn})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st
}

func TestMapErr(t *testing.T) {
	if got := mapErr(nil); got != nil {
		t.Fatalf("mapErr(nil) = %v", got)
	}
	provider := fmt.Errorf("provider: %w", gcsstorage.ErrObjectNotExist)
	got := mapErr(provider)
	for _, want := range []error{filestorage.ErrObjectNotFound, sdk.ErrNotFound, provider, gcsstorage.ErrObjectNotExist} {
		if !errors.Is(got, want) {
			t.Errorf("%v does not retain %v", got, want)
		}
	}
	other := errors.New("provider failure")
	if got := mapErr(other); got != other {
		t.Errorf("provider error changed: %v", got)
	}
}

func TestOpenCanonicalPrefix(t *testing.T) {
	for _, prefix := range []string{"", "tenant", "tenant/", "a/b", "a/b/", ".segovia", " spaced /文件"} {
		t.Run(prefix, func(t *testing.T) {
			st := testStore(t, Config{Prefix: prefix}, func(*http.Request) (*http.Response, error) {
				t.Error("unexpected request")
				return nil, errors.New("unexpected request")
			})
			want := prefix
			if want != "" && !strings.HasSuffix(want, "/") {
				want += "/"
			}
			if st.prefix != want || st.key("nested/file.txt") != want+"nested/file.txt" {
				t.Fatalf("prefix=%q key=%q", st.prefix, st.key("nested/file.txt"))
			}
		})
	}
	for _, prefix := range []string{"/", "/tenant", "tenant//", "a//b", "a/..", "a/../", "a/.", "a/./", "a\\b", "a\x00b"} {
		// Invalid settings must fail before any credential discovery is attempted.
		if _, err := Open(context.Background(), Config{Bucket: "test-bucket", Prefix: prefix}); !errors.Is(err, filestorage.ErrInvalidPath) {
			t.Errorf("prefix %q: %v", prefix, err)
		}
	}
	if _, err := Open(context.Background(), Config{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty bucket: %v", err)
	}
}

func TestOperationsRejectInvalidAndCanceledInputsBeforeIO(t *testing.T) {
	st := &Store{}
	operations := []struct {
		name string
		run  func(context.Context, string) error
	}{
		{"upload", func(ctx context.Context, key string) error { return st.Upload(ctx, key, strings.NewReader("data")) }},
		{"download", func(ctx context.Context, key string) error { _, err := st.Download(ctx, key); return err }},
		{"delete", st.Delete},
		{"exists", func(ctx context.Context, key string) error { _, err := st.Exists(ctx, key); return err }},
		{"size", func(ctx context.Context, key string) error { _, err := st.GetObjectSize(ctx, key); return err }},
		{"range", func(ctx context.Context, key string) error { _, err := st.DownloadRange(ctx, key, 0, 1); return err }},
		{"signed", func(ctx context.Context, key string) error { _, err := st.SignedURL(ctx, key, time.Second); return err }},
		{"resumable", func(ctx context.Context, key string) error {
			_, err := st.InitiateResumableUpload(ctx, key, filestorage.ResumableUploadOptions{})
			return err
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			for _, path := range []string{"", "/absolute", "dir//file", "dir/../file", "dir\\file", "dir/", "nul\x00key"} {
				if err := op.run(context.Background(), path); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("path %q: %v", path, err)
				}
			}
			if err := op.run(ctx, "valid"); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled: %v", err)
			}
		})
	}
	if _, err := st.List(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := st.List(context.Background(), "a/../"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := Open(ctx, Config{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := st.Upload(context.Background(), "valid", nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	for _, r := range [][2]int64{{-1, 1}, {0, -2}, {math.MaxInt64, 2}} {
		if _, err := st.DownloadRange(context.Background(), "valid", r[0], r[1]); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("range %v: %v", r, err)
		}
	}
	for _, expiry := range []time.Duration{0, time.Millisecond, time.Second + 1, 7*24*time.Hour + time.Second} {
		if _, err := st.SignedURL(context.Background(), "valid", expiry); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("expiry %s: %v", expiry, err)
		}
	}
}

func TestOpenEmulatorAndVendorEndpointShareClient(t *testing.T) {
	for _, tc := range []struct{ name, emulator, config, override, want string }{
		{"default", "", "", "", "https://storage.googleapis.com/storage/v1/"},
		{"emulator", "emulator.invalid:4443", "", "", "http://emulator.invalid:4443/storage/v1/"},
		{"config", "emulator.invalid:4443", "https://configured.invalid/storage/v1/", "", "https://configured.invalid/storage/v1/"},
		{"vendor", "", "https://configured.invalid/storage/v1/", "https://override.invalid/storage/v1/", "https://override.invalid/storage/v1/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_EMULATOR_HOST", tc.emulator)
			hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected network")
				return nil, errors.New("unexpected network")
			})}
			opts := []option.ClientOption{option.WithoutAuthentication(), option.WithHTTPClient(hc)}
			if tc.override != "" {
				opts = append(opts, option.WithEndpoint(tc.override))
			}
			st, err := Open(context.Background(), Config{Bucket: "bucket", Endpoint: tc.config}, WithClientOption(opts...))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if st.httpClient != hc || st.endpoint != tc.want {
				t.Fatalf("client shared=%v, endpoint=%q, want %q", st.httpClient == hc, st.endpoint, tc.want)
			}
		})
	}
}
