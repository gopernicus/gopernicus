package gcs

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

type failingReader struct {
	err    error
	closed bool
	read   bool
	cancel context.CancelFunc
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	if r.cancel != nil {
		r.cancel()
	}
	return copy(p, "partial replacement"), r.err
}
func (r *failingReader) Close() error { r.closed = true; return nil }

func TestUploadSourceFailureAbortsBeforeFinalization(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(canceled), func(t *testing.T) {
			var requests atomic.Int32
			st := testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				// A request could reach the transport already canceled. Such a request
				// cannot commit; an uncanceled Close request would publish partial data.
				if r.Context().Err() == nil {
					t.Error("failed source was finalized with a live upload context")
				}
				return nil, r.Context().Err()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sourceErr := errors.New("source disconnected")
			reader := &failingReader{err: sourceErr}
			if canceled {
				reader.cancel = cancel
			}
			err := st.Upload(ctx, "existing", reader)
			if !errors.Is(err, sourceErr) {
				t.Fatalf("source error lost: %v", err)
			}
			if canceled && !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation lost: %v", err)
			}
			if reader.closed {
				t.Error("Upload closed caller reader")
			}
			t.Logf("aborted buffered upload; transport requests=%d", requests.Load())
		})
	}
}

func TestDownloadUsesStoredCompressedBytes(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write([]byte(strings.Repeat("compressible content ", 30)))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	stored := compressed.Bytes()
	st := testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/storage/v1/") {
			return response(r, 200, fmt.Sprintf(`{"size":%q}`, strconv.Itoa(len(stored)))), nil
		}
		if got := r.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Errorf("Accept-Encoding=%q; stored bytes require gzip", got)
		}
		body := stored
		status := http.StatusOK
		if got := r.Header.Get("Range"); got != "" {
			if got != "bytes=3-9" {
				t.Errorf("Range=%q", got)
			}
			body = stored[3:10]
			status = http.StatusPartialContent
		}
		res := response(r, status, string(body))
		res.Header.Set("Content-Encoding", "gzip")
		if status == http.StatusPartialContent {
			res.Header.Set("Content-Range", fmt.Sprintf("bytes 3-9/%d", len(stored)))
		}
		return res, nil
	})
	r, err := st.Download(context.Background(), "compressed")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || !bytes.Equal(got, stored) {
		t.Fatalf("stored download=%x err=%v", got, err)
	}
	r, err = st.DownloadRange(context.Background(), "compressed", 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(r)
	_ = r.Close()
	if err != nil || !bytes.Equal(got, stored[3:10]) {
		t.Fatalf("stored range=%x err=%v", got, err)
	}
	size, err := st.GetObjectSize(context.Background(), "compressed")
	if err != nil || size != int64(len(stored)) {
		t.Fatalf("size=%d err=%v", size, err)
	}
}

func TestRangeEmptyAnd416PreserveErrors(t *testing.T) {
	for _, tc := range []struct {
		name           string
		offset, length int64
		statStatus     int
		size           int64
		wantCode       int
		missing        bool
		wantReads      int
	}{
		{name: "zero", offset: 999, length: 0, statStatus: 200, size: 3, wantReads: 0},
		{name: "zero missing", length: 0, statStatus: 404, missing: true, wantReads: 0},
		{name: "EOF", offset: 3, length: 1, statStatus: 200, size: 3, wantReads: 1},
		{name: "beyond EOF", offset: 8, length: -1, statStatus: 200, size: 3, wantReads: 1},
		{name: "empty object", offset: 0, length: -1, statStatus: 200, size: 0, wantReads: 1},
		{name: "missing after 416", offset: 3, length: 1, statStatus: 404, missing: true, wantReads: 1},
		{name: "permission after 416", offset: 3, length: 1, statStatus: 403, wantCode: 403, wantReads: 1},
		{name: "unexplained 416", offset: 1, length: 1, statStatus: 200, size: 3, wantCode: 416, wantReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, stats := 0, 0
			st := testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.URL.Path, "/storage/v1/") {
					stats++
					return response(r, tc.statStatus, fmt.Sprintf(`{"size":%q}`, strconv.FormatInt(tc.size, 10))), nil
				}
				reads++
				return response(r, 416, `{"error":{"code":416,"message":"range"}}`), nil
			})
			r, err := st.DownloadRange(context.Background(), "object", tc.offset, tc.length)
			if tc.missing {
				if !errors.Is(err, filestorage.ErrObjectNotFound) || !errors.Is(err, sdk.ErrNotFound) {
					t.Fatalf("missing: %v", err)
				}
			} else if tc.wantCode != 0 {
				var provider *googleapi.Error
				if !errors.As(err, &provider) || provider.Code != tc.wantCode {
					t.Fatalf("provider code=%d err=%v", tc.wantCode, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || len(data) != 0 {
					t.Fatalf("empty range=%q err=%v", data, err)
				}
			}
			if reads != tc.wantReads || stats != 1 {
				t.Errorf("reads=%d stats=%d", reads, stats)
			}
		})
	}
}

func TestListUsesLiteralPrefixAndRemovesOnlyConfiguredDirectory(t *testing.T) {
	st := testStore(t, Config{Prefix: "tenant"}, func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("prefix"); got != "tenant/." {
			t.Errorf("prefix=%q", got)
		}
		return response(r, 200, `{"items":[{"name":"tenant/.segovia"},{"name":"tenant/.folder/"},{"name":"tenant/..literal"}]}`), nil
	})
	got, err := st.List(context.Background(), ".")
	if err != nil || strings.Join(got, ",") != ".segovia,..literal" {
		t.Fatalf("list=%v err=%v", got, err)
	}
}

func TestSuccessfulStorageResponsesStillCheckCancellation(t *testing.T) {
	for _, operation := range []string{"exists", "size", "delete", "list", "download"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st := testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
				cancel()
				return response(r, 200, `{"size":"3","items":[]}`), nil
			})
			var err error
			switch operation {
			case "exists":
				_, err = st.Exists(ctx, "key")
			case "size":
				_, err = st.GetObjectSize(ctx, "key")
			case "delete":
				err = st.Delete(ctx, "key")
			case "list":
				_, err = st.List(ctx, "")
			case "download":
				_, err = st.Download(ctx, "key")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
		})
	}
}

func TestListRejectsIncompatibleExistingKeys(t *testing.T) {
	for _, key := range []string{"tenant//absolute", "tenant/a/../file", "tenant/back\\slash"} {
		t.Run(key, func(t *testing.T) {
			st := testStore(t, Config{Prefix: "tenant"}, func(r *http.Request) (*http.Response, error) {
				body, _ := json.Marshal(map[string]any{"items": []map[string]string{{"name": key}}})
				return response(r, 200, string(body)), nil
			})
			got, err := st.List(context.Background(), "")
			if got != nil || !errors.Is(err, filestorage.ErrInvalidPath) {
				t.Fatalf("list=%v err=%v", got, err)
			}
		})
	}
}
