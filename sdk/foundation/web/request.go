package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// validator is the auto-validation seam for [Decode]/[DecodeJSON]: a decoded
// value whose type implements Validate() error is validated in place. It
// composes with [FieldErrors.Err], so a request type can accumulate per-field
// detail in Validate and have it surface through [ErrValidation].
type validator interface {
	Validate() error
}

// Param returns a path parameter from the request.
func Param(r *http.Request, key string) string {
	return r.PathValue(key)
}

// QueryParam returns a query parameter from the request.
func QueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// Decode reads the request body as JSON into a new T and validates it if T
// implements Validate() error. It is an alias for [DecodeJSON].
func Decode[T any](r *http.Request) (T, error) {
	return DecodeJSON[T](r)
}

// DecodeJSON reads the request body as JSON into a new T and validates it if T
// implements Validate() error. When the body overruns a MaxBytesReader limit,
// the returned error wraps *http.MaxBytesError so [ErrValidation] can surface
// the documented 413.
func DecodeJSON[T any](r *http.Request) (T, error) {
	var v T

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return v, fmt.Errorf("read body: %w", err)
	}

	if len(data) == 0 {
		return v, fmt.Errorf("request body is empty")
	}

	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("json decode: %w", err)
	}

	if val, ok := any(&v).(validator); ok {
		if err := val.Validate(); err != nil {
			return v, fmt.Errorf("validation: %w", err)
		}
	}

	return v, nil
}
