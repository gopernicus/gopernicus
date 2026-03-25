// Package gcs provides a Google Cloud Storage implementation of storage.Client.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/infrastructure/storage"
)

var _ storage.Client = (*Client)(nil)

// Client implements the storage.Client interface using Google Cloud Storage.
type Client struct {
	client     *gcsstorage.Client
	bucketName string
}

// New creates a new GCS client.
// If serviceAccountKeyJSON is provided, it uses those credentials.
// Otherwise, it uses Application Default Credentials (e.g., from gcloud auth).
func New(bucketName, projectID, serviceAccountKeyJSON string) (*Client, error) {
	ctx := context.Background()

	var opts []option.ClientOption

	// If service account JSON key is provided, use it.
	if serviceAccountKeyJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(serviceAccountKeyJSON)))
	}
	// Otherwise fall back to Application Default Credentials (ADC).

	client, err := gcsstorage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}

	return &Client{
		client:     client,
		bucketName: bucketName,
	}, nil
}

// Close closes the GCS client.
func (c *Client) Close() error {
	return c.client.Close()
}

// Upload implements storage.Client.
func (c *Client) Upload(ctx context.Context, path string, reader io.Reader) error {
	wc := c.client.Bucket(c.bucketName).Object(path).NewWriter(ctx)

	if _, err := io.Copy(wc, reader); err != nil {
		wc.Close()
		return fmt.Errorf("gcs: %w", errors.Join(storage.ErrUploadFailed, err))
	}

	if err := wc.Close(); err != nil {
		return fmt.Errorf("gcs: %w", errors.Join(storage.ErrUploadFailed, err))
	}

	return nil
}

// Download implements storage.Client.
func (c *Client) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	rc, err := c.client.Bucket(c.bucketName).Object(path).NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil, fmt.Errorf("gcs: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("gcs: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	return rc, nil
}

// Delete implements storage.Client.
func (c *Client) Delete(ctx context.Context, path string) error {
	err := c.client.Bucket(c.bucketName).Object(path).Delete(ctx)
	if err != nil {
		// In GCS, deleting non-existent objects returns an error,
		// but we treat it as success (idempotent).
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil
		}
		return fmt.Errorf("gcs: %w", errors.Join(storage.ErrDeleteFailed, err))
	}

	return nil
}

// Exists implements storage.Client.
func (c *Client) Exists(ctx context.Context, path string) (bool, error) {
	_, err := c.client.Bucket(c.bucketName).Object(path).Attrs(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gcs: check object existence: %w", err)
	}

	return true, nil
}

// List implements storage.Client.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	var results []string

	it := c.client.Bucket(c.bucketName).Objects(ctx, &gcsstorage.Query{
		Prefix: prefix,
	})

	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs: list objects: %w", err)
		}

		// Only include objects (not "directories").
		if !isDirectory(attrs.Name) {
			results = append(results, attrs.Name)
		}
	}

	return results, nil
}

// DownloadRange implements storage.Client.
// Downloads a byte range from an object. Useful for HTTP Range requests.
func (c *Client) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	rc, err := c.client.Bucket(c.bucketName).Object(path).NewRangeReader(ctx, offset, length)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil, fmt.Errorf("gcs: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return nil, fmt.Errorf("gcs: %w", errors.Join(storage.ErrDownloadFailed, err))
	}

	return rc, nil
}

// GetObjectSize implements storage.Client.
func (c *Client) GetObjectSize(ctx context.Context, path string) (int64, error) {
	attrs, err := c.client.Bucket(c.bucketName).Object(path).Attrs(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return 0, fmt.Errorf("gcs: %w", errors.Join(storage.ErrObjectNotFound, err))
		}
		return 0, fmt.Errorf("gcs: get object size: %w", err)
	}

	return attrs.Size, nil
}

// InitiateResumableUpload implements storage.Client.
// Creates a signed POST URL with x-goog-resumable header, POSTs to it,
// and returns the session URI from the Location header.
func (c *Client) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	signedURL, err := c.client.Bucket(c.bucketName).SignedURL(path, &gcsstorage.SignedURLOptions{
		Scheme:      gcsstorage.SigningSchemeV4,
		Method:      "POST",
		Expires:     time.Now().Add(15 * time.Minute),
		ContentType: contentType,
		Headers:     []string{"x-goog-resumable:start"},
	})
	if err != nil {
		return "", fmt.Errorf("gcs: generate resumable signed URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, signedURL, nil)
	if err != nil {
		return "", fmt.Errorf("gcs: create initiation request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-goog-resumable", "start")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gcs: initiate resumable upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("gcs: initiate resumable upload: expected 201, got %d", resp.StatusCode)
	}

	sessionURI := resp.Header.Get("Location")
	if sessionURI == "" {
		return "", fmt.Errorf("gcs: initiate resumable upload: no Location header in response")
	}

	return sessionURI, nil
}

// SignedURL implements storage.Client.
func (c *Client) SignedURL(_ context.Context, path string, expiry time.Duration) (string, error) {
	url, err := c.client.Bucket(c.bucketName).SignedURL(path, &gcsstorage.SignedURLOptions{
		Scheme:  gcsstorage.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(expiry),
	})
	if err != nil {
		return "", fmt.Errorf("gcs: generate signed URL: %w", err)
	}
	return url, nil
}

// isDirectory checks if a GCS object name represents a directory.
func isDirectory(name string) bool {
	return len(name) > 0 && name[len(name)-1] == '/'
}
