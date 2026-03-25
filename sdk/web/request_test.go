package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueryParam(t *testing.T) {
	r := httptest.NewRequest("GET", "/items?page=3&q=hello", nil)

	if got := QueryParam(r, "page"); got != "3" {
		t.Errorf("page = %q, want %q", got, "3")
	}
	if got := QueryParam(r, "q"); got != "hello" {
		t.Errorf("q = %q, want %q", got, "hello")
	}
	if got := QueryParam(r, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}

func TestParam(t *testing.T) {
	// Param delegates to r.PathValue, which requires the request to have
	// been routed through a ServeMux with path parameters.
	mux := http.NewServeMux()

	var captured string
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		captured = Param(r, "id")
	})

	r := httptest.NewRequest("GET", "/users/abc123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if captured != "abc123" {
		t.Errorf("Param(id) = %q, want %q", captured, "abc123")
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	body := `{"name":"Alice","age":30}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))

	got, err := DecodeJSON[payload](r)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}

	if got.Name != "Alice" || got.Age != 30 {
		t.Errorf("got %+v, want {Name:Alice Age:30}", got)
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(""))

	type payload struct{}
	_, err := DecodeJSON[payload](r)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestDecodeJSON_InvalidJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("{not json"))

	type payload struct{ Name string }
	_, err := DecodeJSON[payload](r)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

type validatedPayload struct {
	Email string `json:"email"`
}

func (v *validatedPayload) Validate() error {
	if v.Email == "" {
		return fmt.Errorf("email is required")
	}
	return nil
}

func TestDecodeJSON_Validation(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"email":""}`))

	_, err := DecodeJSON[validatedPayload](r)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDecodeJSON_ValidationPasses(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"email":"a@b.com"}`))

	got, err := DecodeJSON[validatedPayload](r)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.Email != "a@b.com" {
		t.Errorf("Email = %q, want %q", got.Email, "a@b.com")
	}
}

func TestDecode_DelegatesToDecodeJSON(t *testing.T) {
	type payload struct {
		X int `json:"x"`
	}

	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"x":42}`))
	got, err := Decode[payload](r)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.X != 42 {
		t.Errorf("X = %d, want 42", got.X)
	}
}
