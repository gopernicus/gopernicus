package gcs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

const resumableInitTimeout = 15 * time.Minute

// InitiateResumableUpload starts an authenticated JSON API upload session and
// returns its bearer URI. It needs upload credentials, not URL-signing rights.
// The host authorizes Origin and configures bucket CORS for browser use.
func (s *Store) InitiateResumableUpload(ctx context.Context, path string, options filestorage.ResumableUploadOptions) (string, error) {
	if err := validateObject(ctx, path); err != nil {
		return "", err
	}
	if strings.ContainsAny(options.ContentType+options.Origin, "\r\n") {
		return "", fmt.Errorf("gcs: invalid resumable upload headers: %w", sdk.ErrInvalidInput)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, resumableInitTimeout)
		defer cancel()
	}
	metadata := struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType,omitempty"`
	}{Name: s.key(path), ContentType: options.ContentType}
	body, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("gcs: encoding upload metadata: %w", err)
	}
	target, err := url.Parse(googleapi.ResolveRelative(s.endpoint, "/upload/storage/v1/b/"+url.PathEscape(s.bucket)+"/o"))
	if err != nil {
		return "", fmt.Errorf("gcs: invalid upload endpoint: %w", sdk.ErrInvalidInput)
	}
	query := target.Query()
	query.Set("uploadType", "resumable")
	query.Set("name", s.key(path))
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("gcs: building upload initiation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if options.ContentType != "" {
		request.Header.Set("X-Upload-Content-Type", options.ContentType)
	}
	if options.Origin != "" {
		request.Header.Set("Origin", options.Origin)
	}
	response, err := s.httpClient.Do(request)
	if ctx.Err() != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			return "", &resumableRequestError{cause: errors.Join(err, ctx.Err())}
		}
		return "", ctx.Err()
	}
	if err != nil {
		return "", &resumableRequestError{cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// A response body or redirect URL may contain bearer session material.
		return "", &googleapi.Error{Code: response.StatusCode, Message: "gcs: upload initiation failed"}
	}
	location := response.Header.Get("Location")
	session, err := url.Parse(location)
	if err != nil || session.Host == "" || (session.Scheme != "http" && session.Scheme != "https") {
		return "", fmt.Errorf("gcs: upload initiation returned no usable session URI")
	}
	return location, nil
}

// Keep standard transport error chains available without printing a URL from a
// redirect or transport error that may contain a bearer upload-session token.
type resumableRequestError struct{ cause error }

func (*resumableRequestError) Error() string   { return "gcs: upload initiation request failed" }
func (e *resumableRequestError) Unwrap() error { return e.cause }
