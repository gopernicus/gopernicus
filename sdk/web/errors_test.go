package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestErrorConstructors(t *testing.T) {
	tests := []struct {
		name   string
		err    *Error
		status int
		code   string
	}{
		{"BadRequest", ErrBadRequest("bad"), http.StatusBadRequest, "bad_request"},
		{"Unauthorized", ErrUnauthorized("unauth"), http.StatusUnauthorized, "unauthenticated"},
		{"Forbidden", ErrForbidden("denied"), http.StatusForbidden, "permission_denied"},
		{"NotFound", ErrNotFound("missing"), http.StatusNotFound, "not_found"},
		{"Conflict", ErrConflict("exists"), http.StatusConflict, "already_exists"},
		{"Internal", ErrInternal("broke"), http.StatusInternalServerError, "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Status != tt.status {
				t.Errorf("Status = %d, want %d", tt.err.Status, tt.status)
			}
			if tt.err.Code != tt.code {
				t.Errorf("Code = %q, want %q", tt.err.Code, tt.code)
			}
		})
	}
}

func TestErrorImplementsError(t *testing.T) {
	var err error = ErrNotFound("not found")
	if err.Error() != "not found" {
		t.Errorf("Error() = %q, want %q", err.Error(), "not found")
	}
}

func TestNewError(t *testing.T) {
	err := NewError(http.StatusTeapot, "I'm a teapot")
	if err.Status != 418 {
		t.Errorf("Status = %d, want 418", err.Status)
	}
	if err.Code != "" {
		t.Errorf("Code = %q, want empty", err.Code)
	}
}

func TestErrorJSON(t *testing.T) {
	err := ErrNotFound("user not found")
	data, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}

	var m map[string]string
	json.Unmarshal(data, &m)

	if m["message"] != "user not found" {
		t.Errorf("message = %q, want %q", m["message"], "user not found")
	}
	if m["code"] != "not_found" {
		t.Errorf("code = %q, want %q", m["code"], "not_found")
	}
	// Status should not appear in JSON (json:"-").
	if _, ok := m["Status"]; ok {
		t.Error("Status should not be in JSON output")
	}
}
