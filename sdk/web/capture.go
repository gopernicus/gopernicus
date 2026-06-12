package web

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// ResponseCapture wraps http.ResponseWriter to capture response metrics.
type ResponseCapture struct {
	http.ResponseWriter
	StatusCode   int
	BytesWritten int64
	wroteHeader  bool
}

// NewResponseCapture creates a new response capturing wrapper.
func NewResponseCapture(w http.ResponseWriter) *ResponseCapture {
	return &ResponseCapture{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code and passes it through.
func (rc *ResponseCapture) WriteHeader(code int) {
	if !rc.wroteHeader {
		rc.StatusCode = code
		rc.wroteHeader = true
	}
	rc.ResponseWriter.WriteHeader(code)
}

// Write captures the bytes written and passes them through.
func (rc *ResponseCapture) Write(b []byte) (int, error) {
	if !rc.wroteHeader {
		rc.WriteHeader(http.StatusOK)
	}
	n, err := rc.ResponseWriter.Write(b)
	rc.BytesWritten += int64(n)
	return n, err
}

// Unwrap returns the underlying ResponseWriter.
func (rc *ResponseCapture) Unwrap() http.ResponseWriter {
	return rc.ResponseWriter
}

// Flush implements http.Flusher if the underlying writer supports it.
func (rc *ResponseCapture) Flush() {
	if f, ok := rc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker if the underlying writer supports it.
func (rc *ResponseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rc.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijacking not supported")
}
