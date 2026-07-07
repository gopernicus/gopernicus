// Package gcs is the file-storage connector for Google Cloud Storage: it
// implements the sdk/filestorage core Storer port over exactly one third-party
// library-family, cloud.google.com/go/storage (with google.golang.org/api's
// client options and iterator, the storage client's own required surface).
//
// The old fat storage interface is superseded by the sdk's split: Store honors
// the core filestorage.Storer and, because GCS supports them, the two optional
// capability interfaces — filestorage.ResumableUploader (resumable upload
// sessions) and filestorage.SignedURLer (short-lived V4 signed read URLs).
// Callers that only need the core port never see the extras; callers that want
// them reach through filestorage.FileStore's type-assert helpers.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/filestorage/gcs), depending only
// on sdk (for the filestorage sentinels its errors map to) and the Google Cloud
// storage client. Not-found conditions map to filestorage.ErrObjectNotFound so
// errors.Is at the call site stays backend-agnostic. A different vendor (S3, …)
// is a sibling connector, swapped at the composition root.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk/filestorage"
)

// resumableInitTimeout bounds the single HTTP POST that starts a resumable
// upload session when no caller deadline is attached to the context.
const resumableInitTimeout = 15 * time.Minute

// Compile-time proof of the port set this connector satisfies. Store honors the
// core Storer plus both optional capabilities GCS can back.
var (
	_ filestorage.Storer            = (*Store)(nil)
	_ filestorage.ResumableUploader = (*Store)(nil)
	_ filestorage.SignedURLer       = (*Store)(nil)
)

// Config holds the settings Open needs. Hosts populate it from sdk/environment; the
// connector carries no env tags and no functional-options layer beyond Option.
type Config struct {
	// Bucket is the target GCS bucket. Required.
	Bucket string

	// Prefix, when set, is a key root prepended to every path — the GCS analogue
	// of the sdk Disk default's base directory. It scopes one Store to a subtree
	// of a shared bucket (multi-tenant / per-app roots) and is transparent to
	// callers: List strips it back off before returning paths. A missing trailing
	// slash is added.
	Prefix string

	// CredentialsJSON is a service-account key in JSON. When empty, Open falls
	// back to Application Default Credentials (GOOGLE_APPLICATION_CREDENTIALS,
	// gcloud auth, or the workload's metadata identity). A key that carries a
	// private key also lets SignedURL sign locally with no network round trip.
	CredentialsJSON string

	// Endpoint overrides the storage API host — set it to a fake-gcs-server /
	// emulator URL (e.g. "http://localhost:4443/storage/v1/") for tests. When
	// set, Open also disables authentication, matching the emulator's contract.
	Endpoint string
}

// Option configures Open before the client is built.
type Option func(*options)

type options struct {
	clientOpts []option.ClientOption
}

// WithClientOption threads a raw google.golang.org/api/option.ClientOption
// through to storage.NewClient for settings Config does not surface (custom
// HTTP client, quota project, scopes). It is the bring-your-own escape hatch;
// most hosts never need it.
func WithClientOption(opts ...option.ClientOption) Option {
	return func(o *options) {
		o.clientOpts = append(o.clientOpts, opts...)
	}
}

// Store is a GCS-backed filestorage.Storer scoped to one bucket (and optional
// key prefix). Construct it with Open; the zero value is not usable. Close it to
// release the underlying client.
type Store struct {
	client *gcsstorage.Client
	bucket string
	prefix string
}

// Open builds a Store for cfg.Bucket, verifying nothing at construction time —
// the Google Cloud client authenticates lazily on first use, so Open makes no
// network round trip (unlike the datastores' ping-on-open). Pass a ctx with a
// deadline if you add an Option that dials.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("gcs: empty bucket")
	}

	var o options
	if cfg.CredentialsJSON != "" {
		o.clientOpts = append(o.clientOpts, option.WithCredentialsJSON([]byte(cfg.CredentialsJSON)))
	}
	if cfg.Endpoint != "" {
		o.clientOpts = append(o.clientOpts,
			option.WithEndpoint(cfg.Endpoint),
			option.WithoutAuthentication(),
		)
	}
	for _, opt := range opts {
		opt(&o)
	}

	client, err := gcsstorage.NewClient(ctx, o.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("gcs: create client: %w", err)
	}

	return &Store{
		client: client,
		bucket: cfg.Bucket,
		prefix: normalizePrefix(cfg.Prefix),
	}, nil
}

// Close releases the underlying storage client.
func (s *Store) Close() error {
	return s.client.Close()
}

// object returns the bucket object handle for a caller-facing path, applying the
// store prefix.
func (s *Store) object(path string) *gcsstorage.ObjectHandle {
	return s.client.Bucket(s.bucket).Object(s.key(path))
}

// key maps a caller-facing path to a full object name under the store prefix.
func (s *Store) key(path string) string {
	return s.prefix + strings.TrimPrefix(path, "/")
}

// Upload streams reader to the object at path.
func (s *Store) Upload(ctx context.Context, path string, reader io.Reader) error {
	wc := s.object(path).NewWriter(ctx)
	if _, err := io.Copy(wc, reader); err != nil {
		_ = wc.Close()
		return fmt.Errorf("gcs: upload %q: %w", path, mapErr(err))
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("gcs: upload %q: %w", path, mapErr(err))
	}
	return nil
}

// Download opens the object at path. Missing objects map to
// filestorage.ErrObjectNotFound.
func (s *Store) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	rc, err := s.object(path).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs: download %q: %w", path, mapErr(err))
	}
	return rc, nil
}

// Delete removes the object at path. A missing object is not an error
// (idempotent), matching the sdk Disk default.
func (s *Store) Delete(ctx context.Context, path string) error {
	if err := s.object(path).Delete(ctx); err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil
		}
		return fmt.Errorf("gcs: delete %q: %w", path, mapErr(err))
	}
	return nil
}

// Exists reports whether an object exists at path.
func (s *Store) Exists(ctx context.Context, path string) (bool, error) {
	if _, err := s.object(path).Attrs(ctx); err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gcs: exists %q: %w", path, mapErr(err))
	}
	return true, nil
}

// List returns the caller-facing paths of objects under prefix (the store prefix
// is applied and then stripped back off), excluding directory-marker keys.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	it := s.client.Bucket(s.bucket).Objects(ctx, &gcsstorage.Query{Prefix: s.key(prefix)})

	var out []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs: list %q: %w", prefix, mapErr(err))
		}
		if isDirectory(attrs.Name) {
			continue
		}
		out = append(out, strings.TrimPrefix(attrs.Name, s.prefix))
	}
	return out, nil
}

// DownloadRange reads length bytes from offset (length -1 = to end). Missing
// objects map to filestorage.ErrObjectNotFound.
func (s *Store) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	rc, err := s.object(path).NewRangeReader(ctx, offset, length)
	if err != nil {
		return nil, fmt.Errorf("gcs: download range %q: %w", path, mapErr(err))
	}
	return rc, nil
}

// GetObjectSize returns the byte size of the object at path. Missing objects map
// to filestorage.ErrObjectNotFound.
func (s *Store) GetObjectSize(ctx context.Context, path string) (int64, error) {
	attrs, err := s.object(path).Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("gcs: object size %q: %w", path, mapErr(err))
	}
	return attrs.Size, nil
}

// InitiateResumableUpload starts a GCS resumable upload session and returns its
// session URI. It signs a V4 POST URL carrying the x-goog-resumable:start
// header, POSTs to it, and returns the Location header the server hands back.
// The client then PUTs data directly to that URI. Signing needs credentials with
// a private key (a service-account key via Config.CredentialsJSON).
func (s *Store) InitiateResumableUpload(ctx context.Context, path, contentType string) (string, error) {
	signedURL, err := s.client.Bucket(s.bucket).SignedURL(s.key(path), &gcsstorage.SignedURLOptions{
		Scheme:      gcsstorage.SigningSchemeV4,
		Method:      http.MethodPost,
		Expires:     time.Now().Add(resumableInitTimeout),
		ContentType: contentType,
		Headers:     []string{"x-goog-resumable:start"},
	})
	if err != nil {
		return "", fmt.Errorf("gcs: sign resumable url %q: %w", path, err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, resumableInitTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, signedURL, nil)
	if err != nil {
		return "", fmt.Errorf("gcs: build resumable request %q: %w", path, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-goog-resumable", "start")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gcs: initiate resumable upload %q: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("gcs: initiate resumable upload %q: want 201, got %d", path, resp.StatusCode)
	}
	sessionURI := resp.Header.Get("Location")
	if sessionURI == "" {
		return "", fmt.Errorf("gcs: initiate resumable upload %q: missing Location header", path)
	}
	return sessionURI, nil
}

// SignedURL mints a short-lived V4 signed GET URL for the object at path.
// Signing needs credentials with a private key (a service-account key via
// Config.CredentialsJSON); with bare Application Default Credentials the library
// falls back to the IAM SignBlob API. The ctx is accepted for interface parity;
// V4 signing with a local key performs no network round trip.
func (s *Store) SignedURL(_ context.Context, path string, expiry time.Duration) (string, error) {
	url, err := s.client.Bucket(s.bucket).SignedURL(s.key(path), &gcsstorage.SignedURLOptions{
		Scheme:  gcsstorage.SigningSchemeV4,
		Method:  http.MethodGet,
		Expires: time.Now().Add(expiry),
	})
	if err != nil {
		return "", fmt.Errorf("gcs: sign url %q: %w", path, err)
	}
	return url, nil
}

// mapErr converts a GCS driver error into the sdk/filestorage sentinel a caller
// can errors.Is against; a not-found becomes filestorage.ErrObjectNotFound.
// Unrecognized errors pass through unchanged.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return filestorage.ErrObjectNotFound
	}
	return err
}

// normalizePrefix trims a leading slash and ensures a single trailing slash so
// key() can concatenate without producing "//" or a leading "/". Empty stays
// empty.
func normalizePrefix(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

// isDirectory reports whether a GCS object name is a directory-marker key
// (trailing slash). GCS's namespace is flat, but some tools write such markers;
// they are not real objects, so List skips them.
func isDirectory(name string) bool {
	return strings.HasSuffix(name, "/")
}
