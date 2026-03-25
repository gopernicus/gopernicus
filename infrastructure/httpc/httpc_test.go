package httpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func jsonHandler(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
}

func TestClient_Get(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(200, testUser{ID: 1, Name: "alice"}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))

	var user testUser
	if err := client.Get(context.Background(), "/users/1", &user); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if user.Name != "alice" {
		t.Errorf("Name = %q, want %q", user.Name, "alice")
	}
}

func TestClient_Post(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var body testUser
		json.NewDecoder(r.Body).Decode(&body)

		w.WriteHeader(201)
		json.NewEncoder(w).Encode(testUser{ID: 42, Name: body.Name})
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))

	var created testUser
	err := client.Post(context.Background(), "/users", testUser{Name: "bob"}, &created)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if created.ID != 42 || created.Name != "bob" {
		t.Errorf("created = %+v, want {42 bob}", created)
	}
}

func TestClient_Put(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(testUser{ID: 1, Name: "updated"})
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	var user testUser
	if err := client.Put(context.Background(), "/users/1", testUser{Name: "updated"}, &user); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if user.Name != "updated" {
		t.Errorf("Name = %q, want %q", user.Name, "updated")
	}
}

func TestClient_Patch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(testUser{ID: 1, Name: "patched"})
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	var user testUser
	if err := client.Patch(context.Background(), "/users/1", map[string]string{"name": "patched"}, &user); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if user.Name != "patched" {
		t.Errorf("Name = %q, want %q", user.Name, "patched")
	}
}

func TestClient_Delete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	if err := client.Delete(context.Background(), "/users/1", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestClient_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	var user testUser
	err := client.Get(context.Background(), "/users/999", &user)

	if err == nil {
		t.Fatal("expected error for 404")
	}

	fetchErr, ok := IsError(err)
	if !ok {
		t.Fatalf("expected fetch.Error, got %T: %v", err, err)
	}
	if !fetchErr.IsNotFound() {
		t.Errorf("IsNotFound() = false, want true (status=%d)", fetchErr.StatusCode)
	}
	if fetchErr.IsServerError() {
		t.Error("IsServerError() should be false for 404")
	}
}

func TestClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	err := client.Get(context.Background(), "/", nil)

	fetchErr, ok := IsError(err)
	if !ok {
		t.Fatalf("expected fetch.Error, got %T", err)
	}
	if !fetchErr.IsServerError() {
		t.Errorf("IsServerError() = false for status %d", fetchErr.StatusCode)
	}
}

func TestClient_BearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer my-token")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL), WithBearerToken("my-token"))
	client.Get(context.Background(), "/", nil)
}

func TestClient_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !startsWith(auth, "Basic ") {
			t.Errorf("Authorization = %q, want Basic auth", auth)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL), WithBasicAuth("user", "pass"))
	client.Get(context.Background(), "/", nil)
}

func TestClient_APIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "secret" {
			t.Errorf("X-API-Key = %q, want %q", got, "secret")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL), WithAPIKey("X-API-Key", "secret"))
	client.Get(context.Background(), "/", nil)
}

func TestClient_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Custom"); got != "value" {
			t.Errorf("X-Custom = %q, want %q", got, "value")
		}
		if got := r.Header.Get("User-Agent"); got != "my-app/1.0" {
			t.Errorf("User-Agent = %q, want %q", got, "my-app/1.0")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewClient(
		WithBaseURL(srv.URL),
		WithHeader("X-Custom", "value"),
		WithUserAgent("my-app/1.0"),
	)
	client.Get(context.Background(), "/", nil)
}

func TestClient_AbsoluteURL(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(200, testUser{ID: 1, Name: "alice"}))
	defer srv.Close()

	client := NewClient(WithBaseURL("https://should-not-be-used.com"))

	var user testUser
	if err := client.Get(context.Background(), srv.URL+"/users/1", &user); err != nil {
		t.Fatalf("absolute URL should bypass baseURL: %v", err)
	}
}

func TestClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL), WithTimeout(50*time.Millisecond))
	err := client.Get(context.Background(), "/slow", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestGetValue(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(200, testUser{ID: 1, Name: "alice"}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	user, err := GetValue[testUser](client, context.Background(), "/users/1")
	if err != nil {
		t.Fatalf("GetValue: %v", err)
	}
	if user.Name != "alice" {
		t.Errorf("Name = %q, want %q", user.Name, "alice")
	}
}

func TestPostValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body testUser
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(testUser{ID: 99, Name: body.Name})
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	user, err := PostValue[testUser](client, context.Background(), "/users", testUser{Name: "carol"})
	if err != nil {
		t.Fatalf("PostValue: %v", err)
	}
	if user.ID != 99 || user.Name != "carol" {
		t.Errorf("user = %+v, want {99 carol}", user)
	}
}

func TestError_String(t *testing.T) {
	e := &Error{StatusCode: 404, Status: "404 Not Found", Body: []byte(`{"msg":"gone"}`)}
	if got := e.Error(); got == "" {
		t.Error("Error() should not be empty")
	}

	e2 := &Error{StatusCode: 500, Status: "500 Internal Server Error"}
	if got := e2.Error(); got == "" {
		t.Error("Error() with no body should not be empty")
	}
}

func TestIsError_NonFetchError(t *testing.T) {
	_, ok := IsError(context.DeadlineExceeded)
	if ok {
		t.Error("IsError should return false for non-fetch errors")
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
