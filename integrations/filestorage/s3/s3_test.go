// The hermetic tests here run offline with no credentials and no docker: they
// exercise error mapping, the interface contract, and presigned-URL generation
// (a purely local signing operation). The live conformance leg (TestConformance_Live)
// runs the sdk filestoragetest suite against a real S3-compatible service and
// skips LOUDLY when S3_TEST_ENDPOINT is unset, so `make check` stays hermetic
// while a silent green never claims unverified S3 conformance.
//
// Run the live leg against a dockered MinIO:
//
//	docker run --rm -d -p 9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin quay.io/minio/minio server /data
//	S3_TEST_ENDPOINT=http://localhost:9000 \
//	  S3_TEST_ACCESS_KEY_ID=minioadmin \
//	  S3_TEST_SECRET_ACCESS_KEY=minioadmin \
//	  S3_TEST_REGION=us-east-1 \
//	  go test ./...
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"github.com/gopernicus/gopernicus/sdk/filestorage"
	"github.com/gopernicus/gopernicus/sdk/filestorage/filestoragetest"
)

// hermeticStore builds a Store with static credentials and a fake endpoint. No
// network round trip happens at construction, so the returned Store is usable
// for local-only operations (presigning) without a live server.
func hermeticStore(t *testing.T, usePathStyle bool) *Store {
	t.Helper()
	store, err := Open(context.Background(), Config{
		Bucket:          "mybucket",
		Region:          "us-east-1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secretexample",
		Endpoint:        "http://localhost:9000",
		UsePathStyle:    usePathStyle,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}

// TestImplementsPorts pins the compile-time contract at runtime: Store honors the
// core Storer and both optional capability interfaces the sdk advertises via
// type assertion.
func TestImplementsPorts(t *testing.T) {
	var s any = hermeticStore(t, true)
	if _, ok := s.(filestorage.Storer); !ok {
		t.Error("Store does not implement filestorage.Storer")
	}
	if _, ok := s.(filestorage.SignedURLer); !ok {
		t.Error("Store does not implement filestorage.SignedURLer")
	}
	if _, ok := s.(filestorage.ResumableUploader); !ok {
		t.Error("Store does not implement filestorage.ResumableUploader")
	}
}

// TestOpenRejectsEmptyBucket guards the one construction-time precondition.
func TestOpenRejectsEmptyBucket(t *testing.T) {
	if _, err := Open(context.Background(), Config{Region: "us-east-1"}); err == nil {
		t.Fatal("Open() with empty bucket = nil error, want error")
	}
}

// TestIsNotFound covers the S3-error → not-found detection over the typed
// modeled errors, an untyped 404 API error, and a non-404 error that must not
// match.
func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"NoSuchKey", &types.NoSuchKey{}, true},
		{"NotFound", &types.NotFound{}, true},
		{"APIError NoSuchKey", &smithy.GenericAPIError{Code: "NoSuchKey"}, true},
		{"APIError NotFound", &smithy.GenericAPIError{Code: "NotFound"}, true},
		{"other API error", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFound(tc.err); got != tc.want {
				t.Errorf("isNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestMapReadError proves a missing object maps to ErrObjectNotFound and any
// other read failure maps to ErrDownloadFailed — the sentinels the sdk
// conformance suite asserts with errors.Is.
func TestMapReadError(t *testing.T) {
	notFound := mapReadError("k", &types.NoSuchKey{})
	if !errors.Is(notFound, filestorage.ErrObjectNotFound) {
		t.Errorf("mapReadError(NoSuchKey) = %v, want errors.Is(_, ErrObjectNotFound)", notFound)
	}
	other := mapReadError("k", errors.New("network down"))
	if !errors.Is(other, filestorage.ErrDownloadFailed) {
		t.Errorf("mapReadError(other) = %v, want errors.Is(_, ErrDownloadFailed)", other)
	}
	if errors.Is(other, filestorage.ErrObjectNotFound) {
		t.Errorf("mapReadError(other) unexpectedly matched ErrObjectNotFound")
	}
}

// TestSignedURLOffline exercises real SigV4 presigning locally: the presign
// client mints a URL for the bucket/key with no network. Path-style addressing
// must place the bucket in the URL path, which also proves the UsePathStyle
// config option takes effect.
func TestSignedURLOffline(t *testing.T) {
	store := hermeticStore(t, true)

	url, err := store.SignedURL(context.Background(), "dir/file.txt", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}
	if !strings.Contains(url, "/mybucket/dir/file.txt") {
		t.Errorf("SignedURL() = %q, want path-style URL containing /mybucket/dir/file.txt", url)
	}
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Errorf("SignedURL() = %q, want a signed URL (X-Amz-Signature present)", url)
	}

	// Virtual-hosted style must NOT place the bucket in the path.
	vhost := hermeticStore(t, false)
	vurl, err := vhost.SignedURL(context.Background(), "dir/file.txt", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() virtual-host error = %v", err)
	}
	if strings.Contains(vurl, "/mybucket/dir/file.txt") {
		t.Errorf("SignedURL() virtual-host = %q, unexpectedly path-style", vurl)
	}
}

// TestConformance_Live runs the sdk filestoragetest conformance suite against a
// real S3-compatible backend (MinIO, DigitalOcean Spaces, or AWS S3) and skips
// loudly when unconfigured.
func TestConformance_Live(t *testing.T) {
	cfg, ok := liveConfig(t)
	if !ok {
		return
	}
	ctx := context.Background()

	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	setupBucket(t, ctx, store, cfg.Bucket)

	filestoragetest.Run(t, func(t *testing.T) filestorage.Storer { return store })

	t.Run("SignedURLRoundTrip", func(t *testing.T) {
		const path = "signed/hello.txt"
		const content = "signed url payload"
		if err := store.Upload(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("Upload() error = %v", err)
		}
		url, err := store.SignedURL(ctx, path, time.Minute)
		if err != nil {
			t.Fatalf("SignedURL() error = %v", err)
		}
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET signed URL: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read signed URL body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET signed URL status = %d, body = %q", resp.StatusCode, body)
		}
		if string(body) != content {
			t.Errorf("signed URL body = %q, want %q", body, content)
		}
	})

	t.Run("InitiateResumableUpload", func(t *testing.T) {
		id, err := store.InitiateResumableUpload(ctx, "resumable/big.bin", "application/octet-stream")
		if err != nil {
			t.Fatalf("InitiateResumableUpload() error = %v", err)
		}
		if id == "" {
			t.Error("InitiateResumableUpload() returned empty upload ID")
		}
		_, _ = store.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
			Bucket:   aws.String(cfg.Bucket),
			Key:      aws.String("resumable/big.bin"),
			UploadId: aws.String(id),
		})
	})
}

// liveConfig assembles the live Config from S3_TEST_* env vars, or skips loudly
// with copy-pasteable MinIO instructions when S3_TEST_ENDPOINT is unset.
func liveConfig(t *testing.T) (Config, bool) {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set — s3 conformance NOT verified (make check stays hermetic). " +
			"Run MinIO and re-run:\n" +
			"  docker run --rm -d -p 9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin quay.io/minio/minio server /data\n" +
			"  S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_ACCESS_KEY_ID=minioadmin S3_TEST_SECRET_ACCESS_KEY=minioadmin S3_TEST_REGION=us-east-1 go test ./...")
		return Config{}, false
	}
	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return Config{
		Bucket:          fmt.Sprintf("gopernicus-s3-test-%d", time.Now().UnixNano()),
		Region:          region,
		AccessKeyID:     os.Getenv("S3_TEST_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_TEST_SECRET_ACCESS_KEY"),
		Endpoint:        endpoint,
		UsePathStyle:    true, // MinIO and on-host deployments require path style.
	}, true
}

// setupBucket creates a fresh bucket for the live run and registers a cleanup
// that empties and drops it, keeping re-runs repeatable.
func setupBucket(t *testing.T, ctx context.Context, store *Store, bucket string) {
	t.Helper()
	if _, err := store.client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		var owned *types.BucketAlreadyOwnedByYou
		var exists *types.BucketAlreadyExists
		if !errors.As(err, &owned) && !errors.As(err, &exists) {
			t.Fatalf("CreateBucket(%s): %v", bucket, err)
		}
	}
	t.Cleanup(func() {
		emptyBucket(t, store, bucket)
		if _, err := store.client.DeleteBucket(context.Background(), &awss3.DeleteBucketInput{
			Bucket: aws.String(bucket),
		}); err != nil {
			t.Logf("cleanup: DeleteBucket(%s): %v", bucket, err)
		}
	})
}

// emptyBucket deletes every object in bucket so DeleteBucket can succeed.
func emptyBucket(t *testing.T, store *Store, bucket string) {
	t.Helper()
	ctx := context.Background()
	paginator := awss3.NewListObjectsV2Paginator(store.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			t.Logf("cleanup: list objects: %v", err)
			return
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := store.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    obj.Key,
			}); err != nil {
				t.Logf("cleanup: delete %q: %v", *obj.Key, err)
			}
		}
	}
}
