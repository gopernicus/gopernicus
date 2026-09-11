package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/gopernicus/gopernicus/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func requestStore(t *testing.T, handle func(*http.Request) (*http.Response, error)) *Store {
	t.Helper()
	client := awss3.New(awss3.Options{
		Region: "us-east-1", BaseEndpoint: aws.String("http://s3.example.invalid"), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("synthetic-key", "synthetic-secret", ""),
		HTTPClient:  &http.Client{Transport: roundTripFunc(handle)}, RetryMaxAttempts: 1,
	})
	return newTestStore(t, client, "mybucket")
}

func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestValidationBeforeIO(t *testing.T) {
	store := requestStore(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s", r.Method)
		return nil, errors.New("unexpected request")
	})
	ctx := context.Background()
	for _, path := range []string{"", "/abs", "a/../b", "a//b", "a/", `a\b`} {
		tests := []func() error{
			func() error { return store.Upload(ctx, path, strings.NewReader("x")) },
			func() error { _, e := store.Download(ctx, path); return e },
			func() error { return store.Delete(ctx, path) },
			func() error { _, e := store.Exists(ctx, path); return e },
			func() error { _, e := store.DownloadRange(ctx, path, 0, 1); return e },
			func() error { _, e := store.GetObjectSize(ctx, path); return e },
			func() error { _, e := store.SignedURL(ctx, path, time.Minute); return e },
			func() error { _, e := store.InitiateMultipartUpload(ctx, path, ""); return e },
		}
		for i, run := range tests {
			if err := run(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("path %q operation %d: %v", path, i, err)
			}
		}
	}
	for _, expiry := range []time.Duration{-1, 0, time.Nanosecond, time.Second + 1, 8 * 24 * time.Hour} {
		if _, err := store.SignedURL(ctx, "ok", expiry); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("expiry %v: %v", expiry, err)
		}
	}
	for _, r := range [][2]int64{{-1, 1}, {0, -2}, {math.MaxInt64, 2}} {
		if _, err := store.DownloadRange(ctx, "ok", r[0], r[1]); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("range %v: %v", r, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.SignedURL(canceled, "ok", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled signing: %v", err)
	}
	if err := store.Upload(canceled, "ok", panicReader{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled upload: %v", err)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("reader used despite canceled context") }

type failReader struct{ err error }

func (r failReader) Read([]byte) (int, error) { return 0, r.err }

type readOnly struct{ io.Reader }

func TestOpenRejectsPartialCredentials(t *testing.T) {
	for _, cfg := range []Config{{Bucket: "b", AccessKeyID: "id"}, {Bucket: "b", SecretAccessKey: "secret"}} {
		if _, err := Open(context.Background(), cfg); err == nil {
			t.Fatal("incomplete static credentials accepted")
		}
	}
}

func TestOpenResolvesSigningRegion(t *testing.T) {
	for _, name := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_S3"} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "unused-credentials"))
	for _, tc := range []struct {
		name, explicit, environment, shared, want string
	}{
		{name: "missing"},
		{name: "environment", environment: "us-west-2", want: "us-west-2"},
		{name: "shared config", shared: "eu-west-1", want: "eu-west-1"},
		{name: "environment overrides shared", environment: "us-west-2", shared: "eu-west-1", want: "us-west-2"},
		{name: "explicit overrides defaults", explicit: "us-east-1", environment: "us-west-2", shared: "eu-west-1", want: "us-east-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_REGION", tc.environment)
			configPath := filepath.Join(t.TempDir(), "config")
			content := "[default]\n"
			if tc.shared != "" {
				content += "region = " + tc.shared + "\n"
			}
			if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_CONFIG_FILE", configPath)
			store, err := Open(context.Background(), Config{
				Bucket: "test-bucket", Region: tc.explicit,
				AccessKeyID: "synthetic-key", SecretAccessKey: "synthetic-secret",
			})
			if tc.want == "" {
				if store != nil || !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("missing region: store=%v err=%v", store, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			signed, err := store.SignedURL(context.Background(), "key", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(signed)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(parsed.Query().Get("X-Amz-Credential"), "/"+tc.want+"/s3/aws4_request") {
				t.Fatalf("signed URL did not use resolved region %q", tc.want)
			}
		})
	}
}

func TestMissingObjectAndBucket(t *testing.T) {
	for _, code := range []string{"NoSuchKey", "NoSuchBucket", "AccessDenied"} {
		t.Run(code, func(t *testing.T) {
			store := requestStore(t, func(r *http.Request) (*http.Response, error) {
				return response(r, 404, "<Error><Code>"+code+"</Code><Message>missing</Message></Error>"), nil
			})
			_, err := store.Download(context.Background(), "key")
			var provider smithy.APIError
			if !errors.As(err, &provider) || provider.ErrorCode() != code {
				t.Fatalf("provider error lost: %v", err)
			}
			if got := errors.Is(err, sdk.ErrNotFound); got != (code == "NoSuchKey") {
				t.Errorf("root not found=%v, error=%v", got, err)
			}
			if code != "NoSuchKey" {
				if err := store.Delete(context.Background(), "key"); err == nil {
					t.Error("delete hid explicit provider error")
				}
			}
		})
	}
}

func TestDownloadRangeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		offset, length int64
		missing        bool
		headStatus     int
		want           string
		wantErr        bool
	}{
		{"zero", 0, 0, false, 200, "", false}, {"zero_missing", 0, 0, true, 404, "", true},
		{"at_eof", 3, -1, false, 200, "", false}, {"beyond_eof", 9, 1, false, 200, "", false},
		{"truncate", 1, 9, false, 200, "bc", false}, {"empty_object", 0, -1, false, 200, "", false},
		{"range_missing", 9, 1, true, 404, "", true}, {"size_denied", 9, 1, false, 403, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := requestStore(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodHead {
					resp := response(r, tc.headStatus, "")
					resp.Header.Set("Content-Length", "3")
					if tc.name == "empty_object" {
						resp.Header.Set("Content-Length", "0")
					}
					return resp, nil
				}
				if tc.missing {
					return response(r, 404, "<Error><Code>NoSuchKey</Code></Error>"), nil
				}
				if tc.name == "truncate" {
					if got := r.Header.Get("Range"); got != "bytes=1-9" {
						t.Errorf("range=%q", got)
					}
					return response(r, 206, "bc"), nil
				}
				return response(r, 416, "<Error><Code>InvalidRange</Code></Error>"), nil
			})
			body, err := store.DownloadRange(context.Background(), "key", tc.offset, tc.length)
			if tc.wantErr {
				if err == nil {
					body.Close()
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			got, err := io.ReadAll(body)
			if err != nil || string(got) != tc.want {
				t.Fatalf("body=%q, err=%v", got, err)
			}
		})
	}
}

func TestUploadRequestsAndAbort(t *testing.T) {
	cause := errors.New("source disconnected")
	for _, tc := range []struct {
		name                                string
		size                                int
		sourceError, canceled, abortFailure bool
	}{
		{name: "small", size: 7}, {name: "multipart", size: 6 * 1024 * 1024},
		{name: "source_failure_small", size: 7, sourceError: true},
		{name: "source_failure_multipart", size: 6 * 1024 * 1024, sourceError: true},
		{name: "source_and_cleanup_failure", size: 6 * 1024 * 1024, sourceError: true, abortFailure: true},
		{name: "cancel_multipart", size: 6 * 1024 * 1024, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var put, parts, complete, abort, initiate int
			store := requestStore(t, func(r *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				q := r.URL.Query()
				switch {
				case r.Method == http.MethodPost && q.Has("uploads"):
					initiate++
					return response(r, 200, "<InitiateMultipartUploadResult><UploadId>synthetic-upload</UploadId></InitiateMultipartUploadResult>"), nil
				case r.Method == http.MethodPut && q.Has("partNumber"):
					parts++
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						return nil, err
					}
					resp := response(r, 200, "")
					resp.Header.Set("ETag", `"part"`)
					return resp, nil
				case r.Method == http.MethodPost && q.Has("uploadId"):
					complete++
					return response(r, 200, "<CompleteMultipartUploadResult><ETag>done</ETag></CompleteMultipartUploadResult>"), nil
				case r.Method == http.MethodDelete && q.Has("uploadId"):
					abort++
					if r.Context().Err() != nil {
						t.Error("cleanup context canceled")
					}
					if _, ok := r.Context().Deadline(); !ok {
						t.Error("cleanup has no deadline")
					}
					if tc.abortFailure {
						return response(r, 403, "<Error><Code>AccessDenied</Code></Error>"), nil
					}
					return response(r, 204, ""), nil
				case r.Method == http.MethodPut:
					put++
					data, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					if len(data) != tc.size {
						t.Errorf("size=%d,want %d", len(data), tc.size)
					}
					return response(r, 200, ""), nil
				default:
					return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			var reader io.Reader = readOnly{bytes.NewReader(bytes.Repeat([]byte("x"), tc.size))}
			if tc.sourceError {
				reader = io.MultiReader(reader, failReader{cause})
			}
			if tc.canceled {
				reader = io.MultiReader(reader, readerFunc(func([]byte) (int, error) { cancel(); return 0, io.EOF }))
			}
			err := store.Upload(ctx, "overwrite", reader)
			if tc.sourceError && !errors.Is(err, cause) {
				t.Errorf("source cause lost: %v", err)
			}
			if tc.canceled && !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation lost: %v", err)
			}
			if tc.abortFailure {
				var api smithy.APIError
				if !errors.As(err, &api) || api.ErrorCode() != "AccessDenied" {
					t.Errorf("cleanup cause lost: %v", err)
				}
			}
			if !tc.sourceError && !tc.canceled && err != nil {
				t.Fatal(err)
			}
			if tc.sourceError || tc.canceled {
				if put != 0 || complete != 0 {
					t.Errorf("failed source published: put=%d complete=%d", put, complete)
				}
				if initiate > 0 && abort != 1 {
					t.Errorf("abort=%d", abort)
				}
			} else if tc.size > 5*1024*1024 {
				if initiate != 1 || parts != 2 || complete != 1 || abort != 0 {
					t.Errorf("initiate=%d parts=%d complete=%d abort=%d", initiate, parts, complete, abort)
				}
			} else if put != 1 {
				t.Errorf("put=%d", put)
			}
		})
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func runStreamingUploads(t *testing.T, store *Store) {
	t.Helper()
	t.Run("StreamingMultipartAndFailedReplacement", func(t *testing.T) {
		ctx := context.Background()
		key := "streaming/overwrite"
		original := bytes.Repeat([]byte("stream"), 1024*1024)
		if err := store.Upload(ctx, key, readOnly{bytes.NewReader(original)}); err != nil {
			t.Fatal(err)
		}
		cause := errors.New("source disconnected")
		failed := io.MultiReader(readOnly{bytes.NewReader(bytes.Repeat([]byte("partial"), 1024*1024))}, failReader{cause})
		if err := store.Upload(ctx, key, failed); !errors.Is(err, cause) {
			t.Fatalf("source cause: %v", err)
		}
		body, err := store.Download(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(got, original) {
			t.Fatalf("failed replacement changed old object: length=%d read=%v close=%v", len(got), readErr, closeErr)
		}
		uploads, err := store.client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{Bucket: aws.String(store.bucket), Prefix: aws.String(key)})
		if err != nil {
			t.Fatal(err)
		}
		if len(uploads.Uploads) != 0 {
			t.Fatalf("left %d multipart uploads", len(uploads.Uploads))
		}
	})
}

func TestLiteralDotPrefix(t *testing.T) {
	for _, prefix := range []string{".", "..", "nested/.", "nested/.."} {
		t.Run(prefix, func(t *testing.T) {
			parent := prefix[:strings.LastIndex(prefix, "/")+1]
			wanted := prefix + "hidden"
			store := requestStore(t, func(r *http.Request) (*http.Response, error) {
				if got := r.URL.Query().Get("prefix"); got != parent {
					t.Errorf("provider prefix=%q,want %q", got, parent)
				}
				return response(r, 200, "<ListBucketResult><Contents><Key>"+wanted+"</Key></Contents><Contents><Key>"+parent+"other</Key></Contents></ListBucketResult>"), nil
			})
			got, err := store.List(context.Background(), prefix)
			if err != nil || len(got) != 1 || got[0] != wanted {
				t.Fatalf("List=%v,error=%v", got, err)
			}
		})
	}
}

func TestSignedURLCredentialContext(t *testing.T) {
	type marker struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), marker{}, "caller"))
	defer cancel()
	store := requestStore(t, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil })
	options := store.client.Options()
	called := false
	options.Credentials = aws.CredentialsProviderFunc(func(got context.Context) (aws.Credentials, error) {
		called = true
		if got.Value(marker{}) != "caller" {
			t.Error("credential provider lost caller context")
		}
		cancel()
		return aws.Credentials{AccessKeyID: "synthetic", SecretAccessKey: "synthetic"}, nil
	})
	store = newTestStore(t, awss3.New(options), "mybucket")
	if url, err := store.SignedURL(ctx, "key", time.Minute); !errors.Is(err, context.Canceled) || url != "" {
		t.Fatalf("canceled signing returned URL=%t,error=%v", url != "", err)
	}
	if !called {
		t.Fatal("provider was not called")
	}
}

func TestMultipartPreservesProviderAndCleanupErrors(t *testing.T) {
	partErr := errors.New("part connection failed")
	cleanupErr := errors.New("cleanup connection failed")
	store := requestStore(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			return response(r, 200, "<InitiateMultipartUploadResult><UploadId>synthetic-upload</UploadId></InitiateMultipartUploadResult>"), nil
		case r.Method == http.MethodPut:
			return nil, partErr
		case r.Method == http.MethodDelete:
			return nil, cleanupErr
		default:
			t.Errorf("unexpected completion request")
			return nil, errors.New("unexpected request")
		}
	})
	err := store.Upload(context.Background(), "key", readOnly{bytes.NewReader(bytes.Repeat([]byte("x"), 6*1024*1024))})
	if !errors.Is(err, partErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("lost provider or cleanup error: %v", err)
	}
}
