package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
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
	// Param delegates to r.PathValue, which requires the request to have been
	// routed through a ServeMux with path parameters.
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
	for _, body := range []string{"{not json", `{"email":42}`, `{} {}`, " \t\n"} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			got, err := DecodeJSON[validatedPayload](r)
			if err == nil {
				t.Fatal("expected JSON decode error")
			}
			if got.ValidationCalls != 0 {
				t.Errorf("Validate called %d times after decode failure, want 0", got.ValidationCalls)
			}
		})
	}
}

type validatedPayload struct {
	Email           string `json:"email"`
	ValidationCalls int    `json:"-"`
}

func (v *validatedPayload) Validate() error {
	v.ValidationCalls++
	v.Email = strings.TrimSpace(v.Email)
	if v.Email == "" {
		return sdk.Refuse("email", sdk.CodeRequired, "email is required")
	}
	return nil
}

func TestDecodeJSON_Validation(t *testing.T) {
	decoders := []struct {
		name   string
		decode func(*http.Request) (*validatedPayload, error)
	}{
		{"value", func(r *http.Request) (*validatedPayload, error) {
			v, err := DecodeJSON[validatedPayload](r)
			return &v, err
		}},
		{"pointer", DecodeJSON[*validatedPayload]},
	}
	for _, decoder := range decoders {
		for _, email := range []string{"", "a@b.com"} {
			t.Run(decoder.name+"/"+email, func(t *testing.T) {
				r := httptest.NewRequest("POST", "/", strings.NewReader(`{"email":" `+email+` "}`))
				got, err := decoder.decode(r)
				if got == nil {
					t.Fatalf("decoded value is nil: %v", err)
				}
				if got.ValidationCalls != 1 {
					t.Errorf("Validate called %d times, want 1", got.ValidationCalls)
				}
				if got.Email != email {
					t.Errorf("Email = %q, want mutation to %q", got.Email, email)
				}
				if email == "" {
					if !errors.Is(err, sdk.ErrInvalidInput) {
						t.Errorf("error = %v, want sdk.ErrInvalidInput", err)
					}
					var ve *sdk.ValidationError
					if !errors.As(err, &ve) || len(ve.Violations) != 1 || ve.Violations[0].Field != "email" {
						t.Errorf("error = %v, want preserved email violation", err)
					}
				} else if err != nil {
					t.Errorf("DecodeJSON: %v", err)
				}
			})
		}
	}
}

type valueValidatedPayload struct {
	Calls *int `json:"calls"`
}

func (v valueValidatedPayload) Validate() error {
	*v.Calls = *v.Calls + 1
	return nil
}

func TestDecodeJSON_ValueReceiverValidatesOnce(t *testing.T) {
	value, err := DecodeJSON[valueValidatedPayload](httptest.NewRequest("POST", "/", strings.NewReader(`{"calls":0}`)))
	if err != nil {
		t.Fatalf("value decode: %v", err)
	}
	if value.Calls == nil {
		t.Fatal("value target has nil count")
	}
	if *value.Calls != 1 {
		t.Errorf("value receiver validation count = %d, want 1", *value.Calls)
	}

	pointer, err := DecodeJSON[*valueValidatedPayload](httptest.NewRequest("POST", "/", strings.NewReader(`{"calls":0}`)))
	if err != nil {
		t.Fatalf("pointer decode: %v", err)
	}
	if pointer == nil || pointer.Calls == nil {
		t.Fatalf("pointer target = %+v, want non-nil target and count", pointer)
	}
	if *pointer.Calls != 1 {
		t.Errorf("pointer target validation count = %d, want 1", *pointer.Calls)
	}
}

func TestDecodeJSON_RejectsNullBeforeValidation(t *testing.T) {
	for _, body := range []string{"null", " \n null \t"} {
		t.Run(body, func(t *testing.T) {
			value, err := DecodeJSON[validatedPayload](httptest.NewRequest("POST", "/", strings.NewReader(body)))
			if err == nil {
				t.Error("value target accepted null")
			}
			if value.ValidationCalls != 0 {
				t.Errorf("Validate called %d times for null, want 0", value.ValidationCalls)
			}
			if _, err := DecodeJSON[*validatedPayload](httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
				t.Error("pointer target accepted null")
			}
			if _, err := DecodeJSON[any](httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
				t.Error("interface target accepted null")
			}
		})
	}
}

func TestDecodeJSON_OrdinaryTargets(t *testing.T) {
	t.Run("slice", func(t *testing.T) {
		got, err := DecodeJSON[[]int](httptest.NewRequest("POST", "/", strings.NewReader(`[1,2]`)))
		if err != nil || !reflect.DeepEqual(got, []int{1, 2}) {
			t.Fatalf("DecodeJSON = %v, %v; want [1 2], nil", got, err)
		}
	})
	t.Run("map", func(t *testing.T) {
		got, err := DecodeJSON[map[string]*int](httptest.NewRequest("POST", "/", strings.NewReader(`{"count":null}`)))
		if err != nil || !reflect.DeepEqual(got, map[string]*int{"count": nil}) {
			t.Fatalf("DecodeJSON = %v, %v; want map[count:<nil>], nil", got, err)
		}
	})
	t.Run("scalar_pointer", func(t *testing.T) {
		got, err := DecodeJSON[*int](httptest.NewRequest("POST", "/", strings.NewReader(`42`)))
		if err != nil || got == nil || *got != 42 {
			t.Fatalf("DecodeJSON = %v, %v; want pointer to 42, nil", got, err)
		}
	})
	t.Run("string", func(t *testing.T) {
		got, err := DecodeJSON[string](httptest.NewRequest("POST", "/", strings.NewReader(`"null"`)))
		if err != nil || got != "null" {
			t.Fatalf("DecodeJSON = %q, %v; want null string, nil", got, err)
		}
	})
}

func TestDecodeJSONHostBodyLimit(t *testing.T) {
	handler := http.MaxBytesHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := DecodeJSON[map[string]any](r)
		if err != nil {
			RespondJSONError(w, ErrValidation(err))
			return
		}
		RespondJSONOK(w, value)
	}), 16)
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"extra":true}`, 200},
		{`{}` + strings.Repeat(" ", 32), 413},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(test.body)))
		if w.Code != test.want {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
