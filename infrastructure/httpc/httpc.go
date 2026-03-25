// Package httpc provides a type-safe HTTP client for JSON APIs.
//
// # Basic Usage
//
//	client := httpc.NewClient(
//	    httpc.WithBaseURL("https://api.example.com"),
//	    httpc.WithBearerToken("my-token"),
//	)
//
//	// Unmarshal into a pointer
//	var user User
//	err := client.Get(ctx, "/users/123", &user)
//
//	// Or use generics to return the value directly
//	user, err := httpc.GetValue[User](client, ctx, "/users/123")
//
// # Authentication
//
//	httpc.WithBearerToken(token)          // Authorization: Bearer <token>
//	httpc.WithBasicAuth(user, pass)       // Authorization: Basic <base64>
//	httpc.WithAPIKey("X-API-Key", key)    // Custom header
package httpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an HTTP client for JSON APIs.
type Client struct {
	httpClient *http.Client
	baseURL    string
	headers    map[string]string
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the request timeout. Default: 30s.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithBaseURL sets a base URL prepended to all request paths.
// Paths starting with "http://" or "https://" are used as-is.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimSuffix(baseURL, "/")
	}
}

// WithHeaders sets multiple headers (merges with existing).
func WithHeaders(headers map[string]string) Option {
	return func(c *Client) {
		for k, v := range headers {
			c.headers[k] = v
		}
	}
}

// WithHeader sets a single header.
func WithHeader(key, value string) Option {
	return func(c *Client) {
		c.headers[key] = value
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) {
		c.headers["User-Agent"] = userAgent
	}
}

// WithBearerToken sets bearer token authentication.
func WithBearerToken(token string) Option {
	return func(c *Client) {
		c.headers["Authorization"] = "Bearer " + token
	}
}

// WithAPIKey sets a custom API key header.
func WithAPIKey(headerName, key string) Option {
	return func(c *Client) {
		c.headers[headerName] = key
	}
}

// WithBasicAuth sets HTTP Basic Authentication.
func WithBasicAuth(username, password string) Option {
	return func(c *Client) {
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		c.headers["Authorization"] = "Basic " + credentials
	}
}

// WithTransport sets a custom http.RoundTripper on the underlying http.Client.
// Use this to wrap the transport with tracing, retries, or other middleware.
//
// Example with tracing:
//
//	client := httpc.NewClient(
//	    httpc.WithTransport(httpc.NewTracingTransport(tracer, http.DefaultTransport)),
//	)
func WithTransport(transport http.RoundTripper) Option {
	return func(c *Client) {
		c.httpClient.Transport = transport
	}
}

// WithHTTPClient sets a custom underlying http.Client.
// Use this when you need custom transport settings (proxies, TLS, etc.).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// NewClient creates a new fetch client with the given options.
func NewClient(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		headers: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// =============================================================================
// HTTP Methods
// =============================================================================

// Get performs a GET request and unmarshals the JSON response into result.
func (c *Client) Get(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodGet, path, nil, result)
}

// Post performs a POST request with a JSON body and unmarshals the response.
func (c *Client) Post(ctx context.Context, path string, body, result any) error {
	return c.do(ctx, http.MethodPost, path, body, result)
}

// Put performs a PUT request with a JSON body and unmarshals the response.
func (c *Client) Put(ctx context.Context, path string, body, result any) error {
	return c.do(ctx, http.MethodPut, path, body, result)
}

// Patch performs a PATCH request with a JSON body and unmarshals the response.
func (c *Client) Patch(ctx context.Context, path string, body, result any) error {
	return c.do(ctx, http.MethodPatch, path, body, result)
}

// Delete performs a DELETE request. Pass nil for result if no response body is expected.
func (c *Client) Delete(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodDelete, path, nil, result)
}

// =============================================================================
// Generic convenience functions
// =============================================================================

// GetValue performs a GET and returns the unmarshaled value.
func GetValue[T any](c *Client, ctx context.Context, path string) (T, error) {
	var result T
	err := c.Get(ctx, path, &result)
	return result, err
}

// PostValue performs a POST and returns the unmarshaled response.
func PostValue[T any](c *Client, ctx context.Context, path string, body any) (T, error) {
	var result T
	err := c.Post(ctx, path, body, &result)
	return result, err
}

// PutValue performs a PUT and returns the unmarshaled response.
func PutValue[T any](c *Client, ctx context.Context, path string, body any) (T, error) {
	var result T
	err := c.Put(ctx, path, body, &result)
	return result, err
}

// PatchValue performs a PATCH and returns the unmarshaled response.
func PatchValue[T any](c *Client, ctx context.Context, path string, body any) (T, error) {
	var result T
	err := c.Patch(ctx, path, body, &result)
	return result, err
}

// DeleteValue performs a DELETE and returns the unmarshaled response.
func DeleteValue[T any](c *Client, ctx context.Context, path string) (T, error) {
	var result T
	err := c.Delete(ctx, path, &result)
	return result, err
}

// =============================================================================
// Internal
// =============================================================================

func (c *Client) do(ctx context.Context, method, path string, body, result any) error {
	url := c.buildURL(path)

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("httpc: failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("httpc: failed to create request: %w", err)
	}

	for key, value := range c.headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("httpc: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("httpc: failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       respBody,
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("httpc: failed to unmarshal response: %w", err)
		}
	}

	return nil
}

func (c *Client) buildURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if c.baseURL != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return c.baseURL + path
	}
	return path
}

// =============================================================================
// Error
// =============================================================================

// Error represents an HTTP error response.
type Error struct {
	StatusCode int
	Status     string
	Body       []byte
}

func (e *Error) Error() string {
	if len(e.Body) > 0 {
		body := string(e.Body)
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		return fmt.Sprintf("httpc: HTTP %d %s: %s", e.StatusCode, e.Status, body)
	}
	return fmt.Sprintf("httpc: HTTP %d %s", e.StatusCode, e.Status)
}

// IsNotFound returns true if the error is a 404.
func (e *Error) IsNotFound() bool { return e.StatusCode == http.StatusNotFound }

// IsUnauthorized returns true if the error is a 401.
func (e *Error) IsUnauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

// IsForbidden returns true if the error is a 403.
func (e *Error) IsForbidden() bool { return e.StatusCode == http.StatusForbidden }

// IsServerError returns true if the error is a 5xx.
func (e *Error) IsServerError() bool { return e.StatusCode >= 500 }

// IsError checks if an error is an httpc.Error and returns it.
func IsError(err error) (*Error, bool) {
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}
