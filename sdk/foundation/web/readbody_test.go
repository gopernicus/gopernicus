package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// readBody is the test harness: it runs ReadBody against a synthetic POST
// carrying body.
func readBody(t *testing.T, body string, keys ...string) (*Body, error) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	return ReadBody(httptest.NewRecorder(), r, keys...)
}

// violations recovers the accumulated violations from an error, failing the
// test when the error is not a *sdk.ValidationError.
func violations(t *testing.T, err error) []sdk.Violation {
	t.Helper()

	var ve *sdk.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want a *sdk.ValidationError", err, err)
	}
	return ve.Violations
}

func TestReadBody_DeclaredKeys(t *testing.T) {
	body, err := readBody(t, `{"title":"Hello","published":true}`, "title", "published")
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}

	if got := body.Str("title"); got != "Hello" {
		t.Errorf("Str(title) = %q, want %q", got, "Hello")
	}
	if got := body.Bool("published"); !got {
		t.Error("Bool(published) = false, want true")
	}
	if err := body.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

// TestReadBody_EmptyObjectIsValid pins that {} is the domain's call, not the
// reader's.
func TestReadBody_EmptyObjectIsValid(t *testing.T) {
	body, err := readBody(t, `{}`, "title")
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if body.Has("title") {
		t.Error("Has(title) = true on an empty object, want false")
	}
	if err := body.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestReadBody_StructuralFailures(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"Empty", ``, "request body is empty"},
		{"Whitespace", "  \n\t ", "request body is empty"},
		{"Null", `null`, "request body must be a JSON object, not null"},
		{"Array", `[{"title":"Hello"}]`, "request body must be a JSON object"},
		{"String", `"hello"`, "request body must be a JSON object"},
		{"Number", `42`, "request body must be a JSON object"},
		{"Malformed", `{"title":`, "request body must be a JSON object"},
		{"TrailingObject", `{"title":"a"} {"title":"b"}`, "request body must contain exactly one JSON object"},
		{"TrailingGarbage", `{"title":"a"} nonsense`, "request body must contain exactly one JSON object"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := readBody(t, tt.body, "title")
			if body != nil {
				t.Errorf("body = %+v, want nil on a structural failure", body)
			}
			if err == nil {
				t.Fatal("err = nil, want a structural failure")
			}
			if !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("err = %q, want the sentence %q first", err, tt.want)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Error("errors.Is(err, sdk.ErrInvalidInput) = false, want the sentinel last")
			}
			if got := ErrValidation(err); got.Status != http.StatusBadRequest {
				t.Errorf("ErrValidation status = %d, want 400", got.Status)
			}
		})
	}
}

// TestReadBody_UnknownKeysAllCollected pins the smuggling rule: a body must not
// carry a path-owned id, and every undeclared key is named at once.
func TestReadBody_UnknownKeysAllCollected(t *testing.T) {
	body, err := readBody(t, `{"title":"Hello","organization_id":"org_1","admin":true}`, "title")
	if body != nil {
		t.Errorf("body = %+v, want nil when a key is undeclared", body)
	}

	got := violations(t, err)
	if len(got) != 2 {
		t.Fatalf("violations = %+v, want 2 (both unknown keys)", got)
	}
	want := []sdk.Violation{
		{Field: "admin", Code: sdk.CodeUnknownField, Message: `unknown field "admin"`},
		{Field: "organization_id", Code: sdk.CodeUnknownField, Message: `unknown field "organization_id"`},
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("violations[%d] = %+v, want %+v", i, got[i], v)
		}
	}

	rendered := ErrValidation(err)
	if rendered.Status != http.StatusBadRequest || rendered.Code != "validation_failed" {
		t.Errorf("status/code = %d/%q, want 400/validation_failed", rendered.Status, rendered.Code)
	}
	if len(rendered.Fields) != 2 || rendered.Fields[0].Code != sdk.CodeUnknownField {
		t.Errorf("fields = %+v, want both unknown-field entries with codes", rendered.Fields)
	}
}

// TestReadBody_ExpectedUpdatedAtNotImplicit pins that the CAS key is declared
// like any other: sending it undeclared is a named 400.
func TestReadBody_ExpectedUpdatedAtNotImplicit(t *testing.T) {
	_, err := readBody(t, `{"title":"Hello","expected_updated_at":"2026-08-31T12:00:00Z"}`, "title")

	got := violations(t, err)
	if len(got) != 1 || got[0].Field != BodyKeyExpectedUpdatedAt || got[0].Code != sdk.CodeUnknownField {
		t.Errorf("violations = %+v, want one unknown_field for %q", got, BodyKeyExpectedUpdatedAt)
	}
}

func TestReadBody_OversizedBody(t *testing.T) {
	oversized := `{"title":"` + strings.Repeat("a", DefaultBodyLimit) + `"}`

	body, err := readBody(t, oversized, "title")
	if body != nil {
		t.Errorf("body = %+v, want nil on an overrun", body)
	}

	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		t.Fatalf("err = %v (%T), want *http.MaxBytesError in the chain", err, err)
	}
	if got := ErrValidation(err); got.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("ErrValidation status = %d, want 413", got.Status)
	}
}

func TestBody_Has(t *testing.T) {
	body, err := readBody(t, `{"title":"Hello","summary":null}`, "title", "summary", "slug")
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}

	tests := []struct {
		field string
		want  bool
	}{
		{"title", true},
		{"summary", true}, // an explicit null was SENT — that is the clear
		{"slug", false},
	}
	for _, tt := range tests {
		if got := body.Has(tt.field); got != tt.want {
			t.Errorf("Has(%q) = %v, want %v", tt.field, got, tt.want)
		}
	}
}

func TestBody_GetterMatrix(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		get      func(*Body)
		wantCode string // "" = no violation
	}{
		{"StrPresent", `{"f":"x"}`, func(b *Body) {
			if got := b.Str("f"); got != "x" {
				t.Errorf("Str = %q, want x", got)
			}
		}, ""},
		{"StrAbsent", `{}`, func(b *Body) { b.Str("f") }, sdk.CodeRequired},
		{"StrNull", `{"f":null}`, func(b *Body) { b.Str("f") }, sdk.CodeInvalidType},
		{"StrWrongType", `{"f":42}`, func(b *Body) { b.Str("f") }, sdk.CodeInvalidType},

		{"OptStrPresent", `{"f":"x"}`, func(b *Body) {
			if got := b.OptStr("f"); got == nil || *got != "x" {
				t.Errorf("OptStr = %v, want a pointer to x", got)
			}
		}, ""},
		{"OptStrAbsent", `{}`, func(b *Body) {
			if got := b.OptStr("f"); got != nil {
				t.Errorf("OptStr = %v, want nil", *got)
			}
		}, ""},
		{"OptStrNull", `{"f":null}`, func(b *Body) {
			if got := b.OptStr("f"); got != nil {
				t.Errorf("OptStr = %v, want nil", *got)
			}
		}, ""},
		{"OptStrWrongType", `{"f":42}`, func(b *Body) { b.OptStr("f") }, sdk.CodeInvalidType},

		{"BoolPresent", `{"f":true}`, func(b *Body) {
			if !b.Bool("f") {
				t.Error("Bool = false, want true")
			}
		}, ""},
		{"BoolAbsent", `{}`, func(b *Body) { b.Bool("f") }, sdk.CodeRequired},
		{"BoolNull", `{"f":null}`, func(b *Body) { b.Bool("f") }, sdk.CodeInvalidType},
		{"BoolWrongType", `{"f":"true"}`, func(b *Body) { b.Bool("f") }, sdk.CodeInvalidType},

		{"StrsPresent", `{"f":["a","b"]}`, func(b *Body) {
			got := b.Strs("f")
			if len(got) != 2 || got[0] != "a" || got[1] != "b" {
				t.Errorf("Strs = %v, want [a b]", got)
			}
		}, ""},
		{"StrsEmpty", `{"f":[]}`, func(b *Body) {
			if got := b.Strs("f"); len(got) != 0 {
				t.Errorf("Strs = %v, want an empty slice", got)
			}
		}, ""},
		{"StrsAbsent", `{}`, func(b *Body) { b.Strs("f") }, sdk.CodeRequired},
		{"StrsNull", `{"f":null}`, func(b *Body) { b.Strs("f") }, sdk.CodeInvalidType},
		{"StrsWrongType", `{"f":"a"}`, func(b *Body) { b.Strs("f") }, sdk.CodeInvalidType},
		{"StrsWrongElement", `{"f":["a",7]}`, func(b *Body) { b.Strs("f") }, sdk.CodeInvalidType},

		{"DatePresent", `{"f":"2026-08-31"}`, func(b *Body) {
			want := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
			if got := b.Date("f"); !got.Equal(want) || got.Location() != time.UTC {
				t.Errorf("Date = %v, want %v (midnight UTC)", got, want)
			}
		}, ""},
		{"DateAbsent", `{}`, func(b *Body) { b.Date("f") }, sdk.CodeRequired},
		{"DateNull", `{"f":null}`, func(b *Body) { b.Date("f") }, sdk.CodeInvalidType},
		{"DateWrongType", `{"f":20260831}`, func(b *Body) { b.Date("f") }, sdk.CodeInvalidType},
		{"DateBadFormat", `{"f":"31/08/2026"}`, func(b *Body) { b.Date("f") }, sdk.CodeInvalidFormat},
		{"DateIsNotAnInstant", `{"f":"2026-08-31T12:00:00Z"}`, func(b *Body) { b.Date("f") }, sdk.CodeInvalidFormat},

		{"InstantPresent", `{"f":"2026-08-31T12:00:00Z"}`, func(b *Body) {
			want := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			if got := b.Instant("f"); !got.Equal(want) {
				t.Errorf("Instant = %v, want %v", got, want)
			}
		}, ""},
		{"InstantFractional", `{"f":"2026-08-31T12:00:00.123456Z"}`, func(b *Body) {
			want := time.Date(2026, 8, 31, 12, 0, 0, 123456000, time.UTC)
			if got := b.Instant("f"); !got.Equal(want) {
				t.Errorf("Instant = %v, want %v", got, want)
			}
		}, ""},
		{"InstantOffset", `{"f":"2026-08-31T14:00:00+02:00"}`, func(b *Body) {
			want := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			if got := b.Instant("f"); !got.Equal(want) {
				t.Errorf("Instant = %v, want %v", got, want)
			}
		}, ""},
		{"InstantAbsent", `{}`, func(b *Body) { b.Instant("f") }, sdk.CodeRequired},
		{"InstantNull", `{"f":null}`, func(b *Body) { b.Instant("f") }, sdk.CodeInvalidType},
		{"InstantWrongType", `{"f":true}`, func(b *Body) { b.Instant("f") }, sdk.CodeInvalidType},
		{"InstantBadFormat", `{"f":"2026-08-31"}`, func(b *Body) { b.Instant("f") }, sdk.CodeInvalidFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := readBody(t, tt.body, "f")
			if err != nil {
				t.Fatalf("ReadBody: %v", err)
			}

			tt.get(body)

			err = body.Err()
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("Err() = %v, want nil", err)
				}
				return
			}
			got := violations(t, err)
			if len(got) != 1 {
				t.Fatalf("violations = %+v, want exactly 1", got)
			}
			if got[0].Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got[0].Code, tt.wantCode)
			}
			if got[0].Field != "f" {
				t.Errorf("field = %q, want f", got[0].Field)
			}
			if got[0].Message == "" {
				t.Error("message = empty, want a caller-facing sentence")
			}
		})
	}
}

// TestBody_StrsElementIndexNamed pins that the element message names which
// element was wrong.
func TestBody_StrsElementIndexNamed(t *testing.T) {
	body, err := readBody(t, `{"tags":["a","b",7]}`, "tags")
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}

	body.Strs("tags")
	got := violations(t, body.Err())
	if len(got) != 1 {
		t.Fatalf("violations = %+v, want 1", got)
	}
	if !strings.Contains(got[0].Message, "tags[2]") {
		t.Errorf("message = %q, want it to name element 2", got[0].Message)
	}
}

// TestBody_GettersNeverShortCircuit pins the collect-everything contract: one
// pass over a bad body surfaces every problem at once, in getter-call order.
func TestBody_GettersNeverShortCircuit(t *testing.T) {
	body, err := readBody(t, `{"title":42,"tags":"go","starts_at":"nope"}`, "title", "tags", "starts_at", "published")
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}

	if got := body.Str("title"); got != "" {
		t.Errorf("Str(title) = %q, want the zero value", got)
	}
	if got := body.Strs("tags"); got != nil {
		t.Errorf("Strs(tags) = %v, want nil", got)
	}
	if got := body.Date("starts_at"); !got.IsZero() {
		t.Errorf("Date(starts_at) = %v, want the zero value", got)
	}
	if got := body.Bool("published"); got {
		t.Error("Bool(published) = true, want the zero value")
	}

	got := violations(t, body.Err())
	want := []sdk.Violation{
		{Field: "title", Code: sdk.CodeInvalidType, Message: "title must be a string"},
		{Field: "tags", Code: sdk.CodeInvalidType, Message: "tags must be an array of strings"},
		{Field: "starts_at", Code: sdk.CodeInvalidFormat, Message: "starts_at must be a date in YYYY-MM-DD format"},
		{Field: "published", Code: sdk.CodeRequired, Message: "published is required"},
	}
	if len(got) != len(want) {
		t.Fatalf("violations = %+v, want %d", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("violations[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	rendered := ErrValidation(body.Err())
	if len(rendered.Fields) != len(want) {
		t.Errorf("rendered fields = %+v, want %d entries", rendered.Fields, len(want))
	}
}

func TestBody_ExpectedUpdatedAt(t *testing.T) {
	body, err := readBody(t, `{"expected_updated_at":"2026-08-31T12:00:00.123456Z"}`, BodyKeyExpectedUpdatedAt)
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}

	want := time.Date(2026, 8, 31, 12, 0, 0, 123456000, time.UTC)
	if got := body.ExpectedUpdatedAt(); !got.Equal(want) {
		t.Errorf("ExpectedUpdatedAt = %v, want %v", got, want)
	}
	if err := body.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestBody_ExpectedUpdatedAtProblems(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"Absent", `{}`, sdk.CodeRequired},
		{"Null", `{"expected_updated_at":null}`, sdk.CodeInvalidType},
		{"WrongType", `{"expected_updated_at":1756641600}`, sdk.CodeInvalidType},
		{"BadFormat", `{"expected_updated_at":"yesterday"}`, sdk.CodeInvalidFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := readBody(t, tt.body, BodyKeyExpectedUpdatedAt)
			if err != nil {
				t.Fatalf("ReadBody: %v", err)
			}

			if got := body.ExpectedUpdatedAt(); !got.IsZero() {
				t.Errorf("ExpectedUpdatedAt = %v, want the zero value", got)
			}
			got := violations(t, body.Err())
			if len(got) != 1 || got[0].Field != BodyKeyExpectedUpdatedAt || got[0].Code != tt.wantCode {
				t.Errorf("violations = %+v, want one %s on %q", got, tt.wantCode, BodyKeyExpectedUpdatedAt)
			}
		})
	}
}

// ExampleReadBody is the compiled reference for the read → Has → getters →
// Err() → ErrValidation flow. It uses plain locals deliberately: this package
// may not import sdk/foundation/crud (guard G21, tests included), so the
// crud.Some composition recipe lives in that package's own doc.
func ExampleReadBody() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, err := ReadBody(w, r, "title", "summary", BodyKeyExpectedUpdatedAt)
		if err != nil {
			RespondJSONError(w, ErrValidation(err))
			return
		}

		var title string
		if body.Has("title") {
			title = body.Str("title")
		}
		summary := body.OptStr("summary")
		expected := body.ExpectedUpdatedAt()

		if err := body.Err(); err != nil {
			RespondJSONError(w, ErrValidation(err))
			return
		}

		fmt.Println(title, summary == nil, expected.Format(time.RFC3339))
	}

	r := httptest.NewRequest(http.MethodPatch, "/articles/1", strings.NewReader(
		`{"title":"New title","summary":null,"expected_updated_at":"2026-08-31T12:00:00Z"}`,
	))
	handler(httptest.NewRecorder(), r)
	// Output: New title true 2026-08-31T12:00:00Z
}
