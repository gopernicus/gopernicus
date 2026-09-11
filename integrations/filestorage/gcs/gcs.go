// Package gcs implements filestorage.Storer and the optional signed-read and
// resumable-upload capabilities using the Google Cloud Storage client family.
// Hosts construct Store directly and own its Close lifecycle.
package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/api/transport"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

var (
	_ filestorage.Storer            = (*Store)(nil)
	_ filestorage.ResumableUploader = (*Store)(nil)
	_ filestorage.SignedURLer       = (*Store)(nil)
)

// Config holds bucket, namespace and authentication settings for Open.
type Config struct {
	Bucket string
	// Prefix is a canonical directory key. A missing trailing slash is added;
	// leading/repeated slashes and dot/dot-dot segments are rejected.
	Prefix string
	// CredentialsJSON supplies credentials; empty uses the vendor's ADC path.
	// A service-account private key also permits local SignedURL signing when
	// SigningServiceAccount is empty. Open may perform credential discovery I/O.
	CredentialsJSON string
	// Endpoint overrides the storage API URL and disables default authentication
	// for emulator use. Explicit vendor client options are applied afterward.
	Endpoint string
	// SigningServiceAccount explicitly selects IAM SignBlob for this service
	// account email or unique ID, using the configured authenticated HTTP client.
	// Empty selects local signing from CredentialsJSON, if available. Signing
	// identity is never inferred through background metadata/credential calls.
	SigningServiceAccount string
}

// Option configures the vendor client before Open builds it.
type Option func(*options)

type options struct {
	clientOpts []option.ClientOption
}

// WithClientOption preserves the vendor option seam for credentials, scopes,
// quota projects, custom HTTP clients and endpoint overrides. These options
// configure storage and its retained HTTP client. Vendor-only credentials do
// not infer local signing identity; use Config's signing fields for SignedURL.
// Calls append options in order. The option copies the supplied slice, while
// vendor option values and their dependencies retain vendor ownership semantics.
func WithClientOption(opts ...option.ClientOption) Option {
	snapshot := append([]option.ClientOption(nil), opts...)
	return func(o *options) { o.clientOpts = append(o.clientOpts, snapshot...) }
}

// Store owns a storage client scoped to one bucket and optional directory key.
// Construct it with Open; the zero value is not usable.
type Store struct {
	client         *gcsstorage.Client
	httpClient     *http.Client
	endpoint       string
	bucket         string
	prefix         string
	signingAccount string
	privateKey     []byte
}

// Open validates local settings, then builds the storage client and retains its
// configured HTTP transport for authenticated resumable initiation and IAM.
// Credential discovery can perform I/O; callers should supply a startup deadline.
// A nil Gopernicus option returns an error wrapping sdk.ErrInvalidInput.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("gcs: bucket is required: %w", sdk.ErrInvalidInput)
	}
	prefix := cfg.Prefix
	if prefix != "" {
		if err := filestorage.ValidatePath(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, fmt.Errorf("gcs: configured prefix: %w", err)
		}
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
	}
	var o options
	o.clientOpts = []option.ClientOption{
		option.WithScopes(gcsstorage.ScopeFullControl, "https://www.googleapis.com/auth/cloud-platform"),
		internaloption.WithDefaultEndpointTemplate("https://storage.UNIVERSE_DOMAIN/storage/v1/"),
		internaloption.WithDefaultMTLSEndpoint("https://storage.mtls.googleapis.com/storage/v1/"),
		internaloption.WithDefaultUniverseDomain("googleapis.com"),
		// Match storage.NewClient's context-aware auth implementation. Legacy
		// oauth2 TokenSource adapters retain their own cancellation semantics.
		internaloption.EnableNewAuthLibrary(),
	}
	// Mirror storage.NewClient's emulator defaults before applying explicit
	// configuration. Both clients must choose the same endpoint and auth path.
	if host := os.Getenv("STORAGE_EMULATOR_HOST"); host != "" {
		if !strings.Contains(host, "://") {
			host = "http://" + host
		}
		emulator, err := url.Parse(host)
		if err != nil {
			return nil, fmt.Errorf("gcs: invalid emulator endpoint: %w", sdk.ErrInvalidInput)
		}
		emulator.Path = "storage/v1/"
		o.clientOpts = append(o.clientOpts, option.WithoutAuthentication(),
			internaloption.SkipDialSettingsValidation(),
			internaloption.WithDefaultEndpointTemplate(emulator.String()),
			internaloption.WithDefaultMTLSEndpoint(emulator.String()))
	}
	if cfg.CredentialsJSON != "" {
		o.clientOpts = append(o.clientOpts, option.WithCredentialsJSON([]byte(cfg.CredentialsJSON)))
	}
	if cfg.Endpoint != "" {
		o.clientOpts = append(o.clientOpts, option.WithEndpoint(cfg.Endpoint), option.WithoutAuthentication())
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("gcs: nil Option: %w", sdk.ErrInvalidInput)
		}
		opt(&o)
	}

	s := &Store{bucket: cfg.Bucket, prefix: prefix, signingAccount: cfg.SigningServiceAccount}
	if s.signingAccount == "" && cfg.CredentialsJSON != "" {
		var key struct {
			Type        string `json:"type"`
			ClientEmail string `json:"client_email"`
			PrivateKey  string `json:"private_key"`
		}
		if err := json.Unmarshal([]byte(cfg.CredentialsJSON), &key); err != nil {
			return nil, fmt.Errorf("gcs: parsing credentials JSON: %w", err)
		}
		if key.Type == "service_account" && key.ClientEmail != "" && key.PrivateKey != "" {
			s.signingAccount, s.privateKey = key.ClientEmail, []byte(key.PrivateKey)
		}
	}
	httpClient, endpoint, err := transport.NewHTTPClient(ctx, o.clientOpts...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("gcs: creating HTTP client: %w", err)
	}
	// Keep the exact configured transport shared by the storage API and extras,
	// while retaining the vendor options used by storage.NewClient itself.
	clientOpts := append(o.clientOpts, option.WithEndpoint(endpoint), option.WithHTTPClient(httpClient))
	client, err := gcsstorage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("gcs: creating storage client: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	s.client, s.httpClient, s.endpoint = client, httpClient, endpoint
	return s, nil
}

// Close releases storage-client resources and closes idle HTTP connections.
// Finish active operations before closing the store.
func (s *Store) Close() error { return s.client.Close() }

func (s *Store) key(path string) string { return s.prefix + path }

func (s *Store) object(path string) *gcsstorage.ObjectHandle {
	return s.client.Bucket(s.bucket).Object(s.key(path))
}
