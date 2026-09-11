package web

import (
	"encoding/json"
	"io"
	"net/http"
)

// errorRecorder is implemented by response writers that want to capture the
// underlying error for downstream observability (the request logger
// middleware). Defining it here keeps sdk/pkg/web from importing delivery
// packages while still letting transport-level loggers surface the error.
type errorRecorder interface {
	RecordError(err error)
}

// RecordError offers a non-nil error to the first recorder, walking transparent
// Unwrap wrappers. A recorder owns forwarding to any recorder beneath it.
// Render/respond helpers use this seam without logging on their own.
func RecordError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	for {
		if rec, ok := w.(errorRecorder); ok {
			rec.RecordError(err)
			return
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = wrapper.Unwrap()
	}
}

// ---------------------------------------------------------------------------
// JSON responses
// ---------------------------------------------------------------------------

// RespondJSON writes a JSON response with the given status code.
func RespondJSON(w http.ResponseWriter, status int, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		RecordError(w, err)
		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, err = w.Write(data)
	RecordError(w, err)
	return err
}

// RespondJSONOK writes a 200 JSON response.
func RespondJSONOK(w http.ResponseWriter, v any) error {
	return RespondJSON(w, http.StatusOK, v)
}

// RespondJSONCreated writes a 201 JSON response. Convenience for POST handlers.
func RespondJSONCreated(w http.ResponseWriter, v any) error {
	return RespondJSON(w, http.StatusCreated, v)
}

// RespondJSONAccepted writes a 202 JSON response. Convenience for async operations.
func RespondJSONAccepted(w http.ResponseWriter, v any) error {
	return RespondJSON(w, http.StatusAccepted, v)
}

// RespondJSONError writes a JSON error response from an [*Error].
func RespondJSONError(w http.ResponseWriter, err *Error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(err.Status)
	RecordError(w, json.NewEncoder(w).Encode(err))
}

// RespondJSONDomainError maps a domain error to an HTTP error and writes it as
// JSON. This is the standard way to handle domain errors in JSON handlers.
//
// When the mapped status is 5xx, the original (pre-mapping) error is offered to
// the response writer via the [RecordError] seam so the request logger can
// include it on the request log line. The write itself stays unchanged —
// clients still receive the generic "internal error" body.
//
//	if err != nil {
//	    web.RespondJSONDomainError(w, err)
//	    return
//	}
func RespondJSONDomainError(w http.ResponseWriter, err error) {
	mapped := ErrFromDomain(err)
	if mapped.Status >= http.StatusInternalServerError {
		RecordError(w, err)
	}
	RespondJSONError(w, mapped)
}

func respondHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := io.WriteString(w, html)
	RecordError(w, err)
}

// RespondNoContent writes a 204 No Content response.
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// RespondRedirect sends an HTTP redirect.
func RespondRedirect(w http.ResponseWriter, r *http.Request, url string, status int) {
	http.Redirect(w, r, url, status)
}
