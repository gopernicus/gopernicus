// Package s3 implements the sdk filestorage ports over the AWS SDK for Go v2
// (aws-sdk-go-v2) service/s3 client. It backs the core filestorage.Storer port
// plus the optional filestorage.SignedURLer (via the s3 presign client) and
// filestorage.ResumableUploader (via S3 multipart upload) capabilities.
//
// It targets AWS S3 and any S3-compatible service — MinIO and DigitalOcean
// Spaces included — through a custom-endpoint option and a path-style-addressing
// option on Config. It is its own module
// (github.com/gopernicus/gopernicus/integrations/filestorage/s3), depending only on sdk (for the
// filestorage sentinels its errors map to) and the aws-sdk-go-v2 family.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/gopernicus/gopernicus/sdk/filestorage"
)

// Store honors the S3 filestorage contract. It is constructed from a Config via
// Open, or from a caller-supplied *s3.Client via New.
//
// It implements the core filestorage.Storer port and both optional capability
// interfaces the sdk defines: SignedURLer (short-lived presigned GET URLs) and
// ResumableUploader (S3 multipart upload sessions).
var (
	_ filestorage.Storer            = (*Store)(nil)
	_ filestorage.SignedURLer       = (*Store)(nil)
	_ filestorage.ResumableUploader = (*Store)(nil)
)

// Config holds the S3-compatible connection settings for Open. Its `env:` tags
// let a host populate it with sdk/environment.ParseEnvTags; a zero Endpoint targets
// AWS S3 with its standard endpoints. Populating from the environment is a
// convenience, not an import edge — struct-literal construction stays
// first-class.
//
// Endpoint and UsePathStyle are the S3-compatibility seam: point Endpoint at a
// MinIO or DigitalOcean Spaces host and set UsePathStyle when the service
// addresses buckets as path segments (MinIO) rather than virtual-hosted
// subdomains.
type Config struct {
	// Bucket is the S3 bucket every operation targets. Required.
	Bucket string `env:"S3_BUCKET"`

	// Region is the AWS region (or the region a compatible service expects,
	// commonly "us-east-1" for MinIO). Required for request signing.
	Region string `env:"S3_REGION"`

	// AccessKeyID and SecretAccessKey supply static credentials. When both are
	// empty, Open falls back to the AWS default credential chain (environment,
	// shared config, IAM role, ...).
	AccessKeyID     string `env:"S3_ACCESS_KEY_ID"`
	SecretAccessKey string `env:"S3_SECRET_ACCESS_KEY"`

	// Endpoint, when set, overrides the AWS endpoint so the client talks to an
	// S3-compatible service. Examples:
	//   - DigitalOcean Spaces: "https://nyc3.digitaloceanspaces.com"
	//   - MinIO:               "http://localhost:9000"
	// Leave empty for AWS S3.
	Endpoint string `env:"S3_ENDPOINT"`

	// UsePathStyle forces path-style addressing (endpoint/bucket/key) instead of
	// virtual-hosted-style (bucket.endpoint/key). MinIO and other on-host
	// deployments generally require it; most cloud providers accept either.
	UsePathStyle bool `env:"S3_USE_PATH_STYLE"`
}

// Store implements filestorage.Storer over the aws-sdk-go-v2 s3 client.
type Store struct {
	client  *awss3.Client
	presign *awss3.PresignClient
	bucket  string
}

// New wraps a caller-supplied *s3.Client for the given bucket, deriving the
// presign client it needs for SignedURL. Use it to bring your own configured
// client; use Open to build one from a Config.
func New(client *awss3.Client, bucket string) *Store {
	return &Store{
		client:  client,
		presign: awss3.NewPresignClient(client),
		bucket:  bucket,
	}
}

// Open builds a Store from cfg. It loads AWS configuration (static credentials
// when supplied, else the default chain) and constructs an s3 client honoring
// the custom-endpoint and path-style options. It performs no network round
// trip — credentials are resolved lazily on first use — so a misconfigured
// endpoint surfaces on the first operation, not here.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: empty bucket")
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}

	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return New(client, cfg.Bucket), nil
}

// Upload writes data from reader to path in the bucket.
func (s *Store) Upload(ctx context.Context, path string, reader io.Reader) error {
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
		Body:   reader,
	})
	if err != nil {
		return fmt.Errorf("s3: upload %q: %w", path, errors.Join(filestorage.ErrUploadFailed, err))
	}
	return nil
}

// Download opens the object at path. Missing objects map to ErrObjectNotFound.
func (s *Store) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		return nil, mapReadError(path, err)
	}
	return out.Body, nil
}

// Delete removes the object at path. A missing object is not an error.
func (s *Store) Delete(ctx context.Context, path string) error {
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("s3: delete %q: %w", path, errors.Join(filestorage.ErrDeleteFailed, err))
	}
	return nil
}

// Exists reports whether an object exists at path.
func (s *Store) Exists(ctx context.Context, path string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("s3: exists %q: %w", path, err)
	}
	return true, nil
}

// List returns the keys of objects under prefix, skipping directory markers.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	paginator := awss3.NewListObjectsV2Paginator(s.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3: list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || isDirectory(*obj.Key) {
				continue
			}
			out = append(out, *obj.Key)
		}
	}
	return out, nil
}

// DownloadRange reads length bytes from offset (length -1 = to end).
func (s *Store) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	var byteRange string
	if length < 0 {
		byteRange = fmt.Sprintf("bytes=%d-", offset)
	} else {
		byteRange = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
		Range:  aws.String(byteRange),
	})
	if err != nil {
		return nil, mapReadError(path, err)
	}
	return out.Body, nil
}

// GetObjectSize returns the byte size of the object at path.
func (s *Store) GetObjectSize(ctx context.Context, path string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, fmt.Errorf("s3: get object size %q: %w", path, filestorage.ErrObjectNotFound)
		}
		return 0, fmt.Errorf("s3: get object size %q: %w", path, err)
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("s3: get object size %q: content length not available", path)
	}
	return *out.ContentLength, nil
}

// SignedURL returns a short-lived presigned GET URL for reading the object at
// path. It is a local signing operation — no network round trip — so the URL is
// minted even when the object does not (yet) exist.
func (s *Store) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	out, err := s.presign.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	}, awss3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3: presign %q: %w", path, err)
	}
	return out.URL, nil
}

// InitiateResumableUpload starts an S3 multipart upload and returns its upload
// ID as the session identifier. Unlike GCS's resumable session URI (a URL the
// client PUTs bytes to), the S3 session is an upload ID the client uses with
// UploadPart + CompleteMultipartUpload; both satisfy the sdk's
// ResumableUploader contract of "an opaque token that resumes a large upload".
func (s *Store) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	in := &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	out, err := s.client.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload %q: %w", path, err)
	}
	if out.UploadId == nil {
		return "", fmt.Errorf("s3: initiate multipart upload %q: no upload ID in response", path)
	}
	return *out.UploadId, nil
}

// mapReadError maps a GetObject error to the filestorage sentinels: a missing
// object to ErrObjectNotFound, anything else to ErrDownloadFailed.
func mapReadError(path string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("s3: download %q: %w", path, errors.Join(filestorage.ErrObjectNotFound, err))
	}
	return fmt.Errorf("s3: download %q: %w", path, errors.Join(filestorage.ErrDownloadFailed, err))
}

// isNotFound reports whether err is an S3 "object does not exist" error. It
// checks the typed modeled errors (NoSuchKey for GET, NotFound for HEAD) first,
// then falls back to the HTTP 404 status and the generic API error code so
// S3-compatible services that return untyped 404s are handled too.
func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
		return true
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

// isDirectory reports whether a key is an S3 "directory" marker (trailing slash).
func isDirectory(key string) bool {
	return len(key) > 0 && key[len(key)-1] == '/'
}
