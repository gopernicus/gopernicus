package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// validator validates decoded request values in place.
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

// DecodeJSON reads the request body as JSON into a new T and validates it once
// if T or *T implements Validate() error. A top-level JSON null is rejected.
// Unknown fields are accepted using encoding/json's normal matching rules.
// The host owns body limits: wrap the route with http.MaxBytesHandler or set
// r.Body with http.MaxBytesReader before calling. No limit is imposed here.
// When the body overruns a MaxBytesReader limit, the returned error wraps
// *http.MaxBytesError so [ErrValidation] can surface the documented 413.
func DecodeJSON[T any](r *http.Request) (T, error) {
	var v T

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return v, fmt.Errorf("read body: %w", err)
	}

	if len(data) == 0 {
		return v, fmt.Errorf("request body is empty")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return v, fmt.Errorf("request body must not be null")
	}

	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("json decode: %w", err)
	}

	val, ok := any(&v).(validator)
	if !ok {
		val, ok = any(v).(validator)
	}
	if ok {
		if err := val.Validate(); err != nil {
			return v, fmt.Errorf("validation: %w", err)
		}
	}

	return v, nil
}
