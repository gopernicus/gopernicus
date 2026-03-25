// Package testhttp provides an HTTP client for E2E tests.
// It reduces boilerplate around request building, auth headers,
// JSON encoding/decoding, and response assertions.
package testhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Client is an HTTP client for E2E tests with built-in JSON handling
// and authentication support.
type Client struct {
	baseURL    string
	headers    http.Header
	httpClient *http.Client
}

// New creates a test HTTP client with the given base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetBearerToken sets the Authorization header for all subsequent requests.
func (c *Client) SetBearerToken(token string) {
	c.headers.Set("Authorization", "Bearer "+token)
}

// SetAPIKey sets the Authorization header with an API key for all subsequent requests.
func (c *Client) SetAPIKey(key string) {
	c.headers.Set("Authorization", "Bearer "+key)
}

// SetHeader sets a custom header for all subsequent requests.
func (c *Client) SetHeader(key, value string) {
	c.headers.Set(key, value)
}

// Get performs a GET request and returns the response.
func (c *Client) Get(t *testing.T, path string) *Response {
	t.Helper()
	return c.do(t, http.MethodGet, path, nil)
}

// Post performs a POST request with a JSON body and returns the response.
func (c *Client) Post(t *testing.T, path string, body any) *Response {
	t.Helper()
	return c.do(t, http.MethodPost, path, body)
}

// Put performs a PUT request with a JSON body and returns the response.
func (c *Client) Put(t *testing.T, path string, body any) *Response {
	t.Helper()
	return c.do(t, http.MethodPut, path, body)
}

// Patch performs a PATCH request with a JSON body and returns the response.
func (c *Client) Patch(t *testing.T, path string, body any) *Response {
	t.Helper()
	return c.do(t, http.MethodPatch, path, body)
}

// Delete performs a DELETE request and returns the response.
func (c *Client) Delete(t *testing.T, path string) *Response {
	t.Helper()
	return c.do(t, http.MethodDelete, path, nil)
}

func (c *Client) do(t *testing.T, method, path string, body any) *Response {
	t.Helper()

	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err, "marshal request body")
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	require.NoError(t, err, "create request: %s %s", method, path)

	// Copy headers.
	for k, vals := range c.headers {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.httpClient.Do(req)
	require.NoError(t, err, "execute request: %s %s", method, path)

	// Read body fully so connection is released.
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err, "read response body: %s %s", method, path)

	return &Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		RawBody:    respBody,
		method:     method,
		path:       path,
	}
}

// Response wraps an HTTP response with assertion helpers.
type Response struct {
	StatusCode int
	Headers    http.Header
	RawBody    []byte
	method     string
	path       string
}

// RequireStatus asserts the response has the expected status code.
func (r *Response) RequireStatus(t *testing.T, expected int) {
	t.Helper()
	require.Equal(t, expected, r.StatusCode,
		"%s %s: expected status %d, got %d\nBody: %s",
		r.method, r.path, expected, r.StatusCode, string(r.RawBody))
}

// AssertStatus asserts the response has the expected status code (non-fatal).
func (r *Response) AssertStatus(t *testing.T, expected int) {
	t.Helper()
	assert.Equal(t, expected, r.StatusCode,
		"%s %s: expected status %d, got %d\nBody: %s",
		r.method, r.path, expected, r.StatusCode, string(r.RawBody))
}

// JSON decodes the response body into a map.
func (r *Response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal(r.RawBody, &result),
		"decode response JSON: %s %s\nBody: %s", r.method, r.path, string(r.RawBody))
	return result
}

// JSONInto decodes the response body into a typed struct.
func (r *Response) JSONInto(t *testing.T, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.RawBody, v),
		"decode response JSON: %s %s\nBody: %s", r.method, r.path, string(r.RawBody))
}

// String extracts a string value from the response JSON by dot-separated path.
// Example: resp.String(t, "user_id") or resp.String(t, "data.0.user_id")
func (r *Response) String(t *testing.T, path string) string {
	t.Helper()
	v := r.Value(t, path)
	s, ok := v.(string)
	require.True(t, ok, "%s %s: expected string at path %q, got %T: %v",
		r.method, r.path, path, v, v)
	return s
}

// Bool extracts a bool value from the response JSON by dot-separated path.
func (r *Response) Bool(t *testing.T, path string) bool {
	t.Helper()
	v := r.Value(t, path)
	b, ok := v.(bool)
	require.True(t, ok, "%s %s: expected bool at path %q, got %T: %v",
		r.method, r.path, path, v, v)
	return b
}

// Value extracts a value from the response JSON by dot-separated path.
// Supports nested keys: "data.0.user_id" accesses data[0].user_id.
func (r *Response) Value(t *testing.T, path string) any {
	t.Helper()
	data := r.JSON(t)
	return extractPath(t, data, path, r.method, r.path)
}

// Data returns the "data" array from a list response.
func (r *Response) Data(t *testing.T) []any {
	t.Helper()
	v := r.Value(t, "data")
	arr, ok := v.([]any)
	require.True(t, ok, "%s %s: expected array at 'data', got %T",
		r.method, r.path, v)
	return arr
}

// DataLen returns the length of the "data" array from a list response.
func (r *Response) DataLen(t *testing.T) int {
	t.Helper()
	return len(r.Data(t))
}

// extractPath navigates a nested map by dot-separated keys.
func extractPath(t *testing.T, data map[string]any, path, method, reqPath string) any {
	t.Helper()
	parts := strings.Split(path, ".")
	var current any = data

	for _, key := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[key]
			require.True(t, ok, "%s %s: key %q not found in response at path %q\navailable keys: %v",
				method, reqPath, key, path, mapKeys(v))
			current = val
		case []any:
			idx := 0
			_, err := fmt.Sscanf(key, "%d", &idx)
			require.NoError(t, err, "%s %s: expected numeric index at %q in path %q",
				method, reqPath, key, path)
			require.Less(t, idx, len(v), "%s %s: index %d out of bounds (len=%d) at path %q",
				method, reqPath, idx, len(v), path)
			current = v[idx]
		default:
			require.Fail(t, fmt.Sprintf("%s %s: cannot traverse %T at key %q in path %q",
				method, reqPath, current, key, path))
		}
	}

	return current
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
