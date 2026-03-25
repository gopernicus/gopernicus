// Package s3 provides an S3-compatible storage implementation of storage.Client.
// Works with AWS S3, Digital Ocean Spaces, MinIO, and other S3-compatible services.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gopernicus/gopernicus/infrastructure/storage"
)

var _ storage.Client = (*Client)(nil)

// Client implements the storage.Client interface using S3-compatible storage.
type Client struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
}

// New creates a new S3-compatible client.
// If customEndpoint is provided, it uses that endpoint (for DO Spaces, MinIO, etc.).
// Otherwise, it uses standard AWS S3 endpoints.
func New(bucketName, region, accessKeyID, secretAccessKey, customEndpoint string) (*Client, error) {
	ctx := context.Background()

	// Create static credentials provider.
	creds := credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")

	// Load AWS config.
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	// Create S3 client.
	var s3Client *s3.Client
	if customEndpoint != "" {
		s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(customEndpoint)
			o.UsePathStyle = false // Most S3-compatible services use virtual-hosted-style.
		})
	} else {
		s3Client = s3.NewFromConfig(cfg)
	}

	return &Client{
		client:        s3Client,
		presignClient: s3.NewPresignClient(s3Client),
		bucketName:    bucketName,
	}, nil
}

// Upload implements storage.Client.
func (c *Client) Upload(ctx context.Context, path string, reader io.Reader) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
		Body:   reader,
	})

	if err != nil {
		return fmt.Errorf("s3: %w", errors.Join(storage.ErrUploadFailed, err))
	}

	return nil
}

// Download implements storage.Client.
func (c *Client) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
	})

	if err != nil {
		var noSuchKey *types.NoSuchKey
		var notFound *types.NotFound
		if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
			return nil, fmt.Errorf("s3: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("s3: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	return result.Body, nil
}

// Delete implements storage.Client.
func (c *Client) Delete(ctx context.Context, path string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
	})

	if err != nil {
		// Deletion is idempotent — deleting non-existent object is not an error.
		var noSuchKey *types.NoSuchKey
		var notFound *types.NotFound
		if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("s3: %w", errors.Join(storage.ErrDeleteFailed, err))
	}

	return nil
}

// Exists implements storage.Client.
func (c *Client) Exists(ctx context.Context, path string) (bool, error) {
	_, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
	})

	if err != nil {
		var noSuchKey *types.NoSuchKey
		var notFound *types.NotFound
		if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
			return false, nil
		}
		return false, fmt.Errorf("s3: check object existence: %w", err)
	}

	return true, nil
}

// List implements storage.Client.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	var results []string

	paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucketName),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3: list objects: %w", err)
		}

		for _, obj := range page.Contents {
			if obj.Key != nil {
				// Only include objects (not "directories").
				if !isDirectory(*obj.Key) {
					results = append(results, *obj.Key)
				}
			}
		}
	}

	return results, nil
}

// DownloadRange implements storage.Client.
// Downloads a byte range from an object. Useful for HTTP Range requests.
func (c *Client) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	// Build the Range header value.
	var rangeValue string
	if length == -1 {
		rangeValue = fmt.Sprintf("bytes=%d-", offset)
	} else {
		rangeValue = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}

	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
		Range:  aws.String(rangeValue),
	})

	if err != nil {
		var noSuchKey *types.NoSuchKey
		var notFound *types.NotFound
		if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
			return nil, fmt.Errorf("s3: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("s3: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	return result.Body, nil
}

// GetObjectSize implements storage.Client.
func (c *Client) GetObjectSize(ctx context.Context, path string) (int64, error) {
	result, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
	})

	if err != nil {
		var noSuchKey *types.NoSuchKey
		var notFound *types.NotFound
		if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
			return 0, fmt.Errorf("s3: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return 0, fmt.Errorf("s3: get object size: %w", err)
	}

	if result.ContentLength == nil {
		return 0, fmt.Errorf("s3: content length not available")
	}

	return *result.ContentLength, nil
}

// InitiateResumableUpload implements storage.Client.
// TODO: S3 multipart upload returns an upload ID, not a session URI like GCS.
// The caller must use UploadPart + CompleteMultipartUpload with this ID,
// which differs from GCS where the caller PUTs bytes directly to the session URI.
// This needs further alignment to unify the resumable upload semantics across backends.
func (c *Client) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	result, err := c.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(c.bucketName),
		Key:         aws.String(path),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: %w", err)
	}

	if result.UploadId == nil {
		return "", fmt.Errorf("s3: initiate multipart upload: no upload ID in response")
	}

	return *result.UploadId, nil
}

// SignedURL implements storage.Client.
func (c *Client) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	result, err := c.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(path),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3: generate signed URL: %w", err)
	}

	return result.URL, nil
}

// isDirectory checks if an S3 object key represents a directory.
func isDirectory(key string) bool {
	return len(key) > 0 && key[len(key)-1] == '/'
}
