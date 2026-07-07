package web

import (
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
)

// errorRecorder is implemented by response writers that want to capture the
// underlying error for downstream observability (the request logger
// middleware). Defining it here keeps sdk/web from importing delivery
// packages while still letting transport-level loggers surface the error.
type errorRecorder interface {
	RecordError(err error)
}

// RecordError offers err to the response writer's error recorder, if it
// implements one. Used by render/respond helpers so a 5xx surfaces on the
// request log line without sdk/web logging on its own.
func RecordError(w http.ResponseWriter, err error) {
	if rec, ok := w.(errorRecorder); ok {
		rec.RecordError(err)
	}
}

// ---------------------------------------------------------------------------
// JSON responses
// ---------------------------------------------------------------------------

// RespondJSON writes a JSON response with the given status code.
func RespondJSON(w http.ResponseWriter, status int, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, err = w.Write(data)
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
	json.NewEncoder(w).Encode(err)
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

// RespondStream copies an io.Reader to the response. The caller is responsible
// for closing the reader if needed. If contentType is empty, it defaults to
// application/octet-stream.
func RespondStream(w http.ResponseWriter, status int, contentType string, r io.Reader) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, err := io.Copy(w, r)
	return err
}

// RespondText writes a plain text response.
func RespondText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(text))
}

// RespondHTML writes an HTML response.
func RespondHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(html))
}

// RespondRaw writes a response with a custom content type.
func RespondRaw(w http.ResponseWriter, status int, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(data)
}

// RespondNoContent writes a 204 No Content response.
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// RespondRedirect sends an HTTP redirect.
func RespondRedirect(w http.ResponseWriter, r *http.Request, url string, status int) {
	http.Redirect(w, r, url, status)
}

// RespondFile serves a file from an fs.FS. It sets Content-Type based on the
// file extension and uses http.ServeContent for range-request support.
func RespondFile(w http.ResponseWriter, r *http.Request, fileFS fs.FS, name string) {
	f, err := fileFS.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}

	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, stat.ModTime(), seeker)
	} else {
		w.WriteHeader(http.StatusOK)
		io.Copy(w, f)
	}
}
