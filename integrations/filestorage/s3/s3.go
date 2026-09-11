// Package s3 implements the sdk filestorage ports over the AWS SDK for Go v2
// (aws-sdk-go-v2) service/s3 client. It backs the core filestorage.Storer port
// plus the optional filestorage.SignedURLer via the s3 presign client.
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
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

// Store honors the S3 filestorage contract. It is constructed from a Config via
// Open, or from a caller-supplied *s3.Client via New.
//
// Its signed GET URLs implement SignedURLer. Multipart initiation is a concrete
// S3 operation because an upload ID is not a resumable session URI.
var (
	_ filestorage.Storer      = (*Store)(nil)
	_ filestorage.SignedURLer = (*Store)(nil)
)

// Config holds the S3-compatible connection settings for Open. Its `env:` tags
// let a host populate it with sdk/pkg/environment.ParseEnvTags; a zero Endpoint targets
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
	// commonly "us-east-1" for MinIO). Empty uses the AWS configuration chain;
	// Open requires that chain to resolve a nonempty signing region.
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
// client; use Open to build one from a Config. It validates the client and bucket
// without I/O; the caller retains ownership and signing configuration.
func New(client *awss3.Client, bucket string) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("s3: client is required: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("s3: bucket is required: %w", sdk.ErrInvalidInput)
	}
	return &Store{
		client:  client,
		presign: awss3.NewPresignClient(client),
		bucket:  bucket,
	}, nil
}

// Open builds a Store from cfg. It loads AWS configuration (static credentials
// when supplied, else the default chain) and constructs an s3 client honoring
// the custom-endpoint and path-style options. Configuration and credential
// discovery may use the network. Open does not check bucket existence.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (cfg.AccessKeyID == "") != (cfg.SecretAccessKey == "") {
		return nil, fmt.Errorf("s3: static access key ID and secret must be supplied together: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("s3: bucket is required: %w", sdk.ErrInvalidInput)
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
	if awsCfg.Region == "" {
		return nil, fmt.Errorf("s3: signing region is required: %w", sdk.ErrInvalidInput)
	}

	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return New(client, cfg.Bucket)
}

// Upload writes a stream using bounded 5 MiB parts and two concurrent requests.
// Uploads are limited to S3's 10,000 parts (about 48.8 GiB).
func (s *Store) Upload(ctx context.Context, path string, reader io.Reader) error {
	if err := validateObject(ctx, path); err != nil {
		return err
	}
	if reader == nil {
		return fmt.Errorf("s3: upload %q: nil reader: %w", path, sdk.ErrInvalidInput)
	}
	input := &uploadReader{ctx: ctx, reader: reader}
	client := &uploadClient{Client: s.client}
	uploader := transfermanager.New(client, func(o *transfermanager.Options) {
		o.PartSizeBytes = 5 * 1024 * 1024
		o.Concurrency = 2
	})
	_, err := uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
		Body:   input,
	})
	if err != nil {
		return fmt.Errorf("s3: upload %q: %w", path, errors.Join(err, input.err, ctx.Err(), client.abortErr))
	}
	return nil
}

// uploadReader checks cancellation around cooperative reads and preserves source
// errors even if a concurrent part request fails first. It never owns the input.
type uploadReader struct {
	ctx    context.Context
	reader io.Reader
	err    error
}

func (r *uploadReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	if canceled := r.ctx.Err(); canceled != nil {
		return n, errors.Join(err, canceled)
	}
	return n, err
}

// The pinned transfer manager aborts with the canceled upload context and loses
// error chains when cleanup fails. Keep cleanup bounded and report its error at
// the Store boundary alongside the original upload error.
type uploadClient struct {
	*awss3.Client
	abortErr error
}

func (c *uploadClient) AbortMultipartUpload(ctx context.Context, in *awss3.AbortMultipartUploadInput, opts ...func(*awss3.Options)) (*awss3.AbortMultipartUploadOutput, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := c.Client.AbortMultipartUpload(cleanupCtx, in, opts...)
	if err != nil {
		c.abortErr = fmt.Errorf("abort multipart upload: %w", err)
		return &awss3.AbortMultipartUploadOutput{}, nil
	}
	return out, nil
}

// Download opens the object at path. Missing objects map to ErrObjectNotFound.
func (s *Store) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := validateObject(ctx, path); err != nil {
		return nil, err
	}
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
	if err := validateObject(ctx, path); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("s3: delete %q: %w", path, err)
	}
	return nil
}

// Exists reports whether an object exists at path.
func (s *Store) Exists(ctx context.Context, path string) (bool, error) {
	if err := validateObject(ctx, path); err != nil {
		return false, err
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := filestorage.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	// MinIO rejects dot segments even in literal prefix queries. Query their
	// containing prefix and apply the final partial segment locally.
	queryPrefix := prefix
	last := prefix[strings.LastIndex(prefix, "/")+1:]
	if last == "." || last == ".." {
		queryPrefix = prefix[:len(prefix)-len(last)]
	}
	var out []string
	paginator := awss3.NewListObjectsV2Paginator(s.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(queryPrefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3: list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || isDirectory(*obj.Key) || !strings.HasPrefix(*obj.Key, prefix) {
				continue
			}
			if err := filestorage.ValidatePath(*obj.Key); err != nil {
				return nil, fmt.Errorf("s3: listed key %q: %w", *obj.Key, err)
			}
			out = append(out, *obj.Key)
		}
	}
	return out, nil
}

// DownloadRange reads length bytes from offset, truncating at EOF.
func (s *Store) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	if err := validateObject(ctx, path); err != nil {
		return nil, err
	}
	if err := filestorage.ValidateRange(offset, length); err != nil {
		return nil, err
	}
	if length == 0 {
		if _, err := s.GetObjectSize(ctx, path); err != nil {
			return nil, err
		}
		return io.NopCloser(strings.NewReader("")), nil
	}
	byteRange := fmt.Sprintf("bytes=%d-", offset)
	if length > 0 {
		byteRange = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(path), Range: aws.String(byteRange),
	})
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) && response.HTTPStatusCode() == http.StatusRequestedRangeNotSatisfiable {
			size, sizeErr := s.GetObjectSize(ctx, path)
			if sizeErr != nil {
				return nil, sizeErr
			}
			if offset >= size {
				return io.NopCloser(strings.NewReader("")), nil
			}
		}
		return nil, mapReadError(path, err)
	}
	return out.Body, nil
}

// GetObjectSize returns the byte size of the object at path.
func (s *Store) GetObjectSize(ctx context.Context, path string) (int64, error) {
	if err := validateObject(ctx, path); err != nil {
		return 0, err
	}
	out, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, fmt.Errorf("s3: get object size %q: %w", path, errors.Join(filestorage.ErrObjectNotFound, err))
		}
		return 0, fmt.Errorf("s3: get object size %q: %w", path, err)
	}
	if out.ContentLength == nil {
		return 0, fmt.Errorf("s3: get object size %q: content length not available", path)
	}
	return *out.ContentLength, nil
}

// SignedURL returns a short-lived presigned GET URL for reading the object at
// path. Signing may retrieve credentials over the network; it does not check
// object existence. Expiry must be whole seconds from one second to seven days.
func (s *Store) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if err := validateObject(ctx, path); err != nil {
		return "", err
	}
	if err := filestorage.ValidateExpiry(expiry); err != nil {
		return "", err
	}
	out, err := s.presign.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(path),
	}, awss3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3: presign %q: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.URL, nil
}

// InitiateMultipartUpload returns an S3 upload ID. The caller must upload parts
// and then complete or abort the upload using the S3 API and the same bucket/key.
// It does not implement filestorage.ResumableUploader's client PUT session URI.
func (s *Store) InitiateMultipartUpload(ctx context.Context, path, contentType string) (string, error) {
	if err := validateObject(ctx, path); err != nil {
		return "", err
	}
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
	if out.UploadId == nil || *out.UploadId == "" {
		return "", fmt.Errorf("s3: initiate multipart upload %q: no upload ID in response", path)
	}
	return *out.UploadId, nil
}

// mapReadError adds the shared not-found kind without losing provider errors.
func mapReadError(path string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("s3: download %q: %w", path, errors.Join(filestorage.ErrObjectNotFound, err))
	}
	return fmt.Errorf("s3: download %q: %w", path, err)
}

// Explicit provider errors take precedence over a generic HTTP 404. A bare
// HEAD 404 cannot distinguish an absent key from an absent bucket.
func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		case "":
		default:
			return false
		}
	}
	var response *smithyhttp.ResponseError
	return errors.As(err, &response) && response.HTTPStatusCode() == http.StatusNotFound
}

func validateObject(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return filestorage.ValidatePath(path)
}

// isDirectory reports whether a key is an S3 "directory" marker (trailing slash).
func isDirectory(key string) bool {
	return len(key) > 0 && key[len(key)-1] == '/'
}
