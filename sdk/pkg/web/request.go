package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gopernicus/gopernicus/sdk"
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

// DecodeJSON is [DecodeJSONLimit] bounded by [DefaultBodyLimit] (1 MiB). A
// route that needs a larger body calls DecodeJSONLimit with its own number; an
// outer http.MaxBytesReader larger than the default no longer governs.
func DecodeJSON[T any](r *http.Request) (T, error) {
	return DecodeJSONLimit[T](r, DefaultBodyLimit)
}

// DecodeJSONLimit reads at most limit bytes of the request body as JSON into a
// new T and validates it once if T or *T implements Validate() error. Unknown
// fields are accepted using encoding/json's normal matching rules.
//
// Failures carry fixed text, never encoding/json's (which names Go types and
// struct fields):
//
//   - an overrun wraps *http.MaxBytesError, so [ErrValidation] answers 413;
//   - an empty body, a top-level JSON null, or a body that does not decode into
//     T wraps sdk.ErrInvalidInput with [ReadBody]'s sentence as its prefix;
//   - a Validate() error keeps its own text and kind.
//
// A limit <= 0 is refused with sdk.ErrInvalidInput: there is no unbounded mode.
func DecodeJSONLimit[T any](r *http.Request, limit int64) (T, error) {
	var v T

	if limit <= 0 {
		return v, fmt.Errorf("decode json: body limit must be positive: %w", sdk.ErrInvalidInput)
	}

	// A nil writer only loses net/http's connection-close hint on overrun; the
	// read still fails with *http.MaxBytesError.
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return v, fmt.Errorf("read body: %w", err)
		}
		return v, fmt.Errorf("read body: %w", sdk.ErrInvalidInput)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return v, fmt.Errorf("request body is empty: %w", sdk.ErrInvalidInput)
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return v, fmt.Errorf("request body must be a JSON object, not null: %w", sdk.ErrInvalidInput)
	}

	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("request body must be a JSON object: %w", sdk.ErrInvalidInput)
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
