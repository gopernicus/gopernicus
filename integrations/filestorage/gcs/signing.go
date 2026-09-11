package gcs

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

// SignedURL creates a V4 signed GET URL without checking object existence.
// Explicit SigningServiceAccount uses IAM with ctx; otherwise CredentialsJSON
// must supply a local service-account key. No identity lookup runs in background.
func (s *Store) SignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if err := validateObject(ctx, path); err != nil {
		return "", err
	}
	if err := filestorage.ValidateExpiry(expiry); err != nil {
		return "", err
	}
	if s.signingAccount == "" {
		return "", fmt.Errorf("gcs: SignedURL requires SigningServiceAccount or a service-account key in CredentialsJSON: %w", sdk.ErrInvalidInput)
	}
	opts := &gcsstorage.SignedURLOptions{Scheme: gcsstorage.SigningSchemeV4,
		Method: http.MethodGet, Expires: time.Now().Add(expiry), GoogleAccessID: s.signingAccount}
	if len(s.privateKey) != 0 {
		opts.PrivateKey = s.privateKey
	} else {
		opts.SignBytes = func(data []byte) ([]byte, error) { return s.signBytes(ctx, data) }
	}
	signed, err := gcsstorage.SignedURL(s.bucket, s.key(path), opts)
	if ctx.Err() != nil {
		return "", errors.Join(err, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("gcs: signing read URL: %w", err)
	}
	return signed, nil
}

func (s *Store) signBytes(ctx context.Context, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service, err := iamcredentials.NewService(ctx, option.WithHTTPClient(s.httpClient))
	if err != nil {
		return nil, fmt.Errorf("gcs: creating IAM signer: %w", err)
	}
	response, err := service.Projects.ServiceAccounts.SignBlob(
		"projects/-/serviceAccounts/"+s.signingAccount,
		&iamcredentials.SignBlobRequest{Payload: base64.StdEncoding.EncodeToString(data)},
	).Context(ctx).Do()
	if ctx.Err() != nil {
		return nil, errors.Join(err, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("gcs: IAM signing: %w", err)
	}
	blob, err := base64.StdEncoding.DecodeString(response.SignedBlob)
	if err != nil {
		return nil, fmt.Errorf("gcs: decoding IAM signature: %w", err)
	}
	if len(blob) == 0 {
		return nil, fmt.Errorf("gcs: IAM returned an empty signature")
	}
	return blob, nil
}
