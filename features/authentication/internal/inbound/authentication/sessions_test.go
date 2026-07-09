package authentication

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRegisterRoute(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"a@example.com","password":"password123","display_name":"A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["email"] != "a@example.com" {
		t.Errorf("email = %v", resp["email"])
	}
}

func TestRegisterRouteBadJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
}

func TestRegisterRouteShortPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"b@example.com","password":"short","display_name":"B"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("short password status = %d, want 400", rec.Code)
	}
}

func TestLoginRouteSetsCookie(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"c@example.com","password":"password123","display_name":"C"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"c@example.com","password":"password123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sessionCookie(rec) == nil {
		t.Error("login did not set a session cookie")
	}
}

func TestLoginRouteWrongPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"d@example.com","password":"password123","display_name":"D"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"d@example.com","password":"wrongpass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}
}

func TestLoginRouteRateLimited(t *testing.T) {
	h := newTestHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/login", `{"email":"e@example.com","password":"password123"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited status = %d, want 429", rec.Code)
	}
}

func TestVerifyRouteUnknown(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/verify", `{"code":"nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("verify unknown status = %d, want 404", rec.Code)
	}
}

func TestForgotPasswordRouteAlwaysAccepted(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/forgot", `{"email":"ghost@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("forgot status = %d, want 202", rec.Code)
	}
}

func TestResetPasswordRouteUnknownToken(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/reset", `{"token":"nope","password":"newpassword"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("reset unknown token status = %d, want 404", rec.Code)
	}
}

func TestLogoutRouteRequiresSession(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/logout", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("logout without session status = %d, want 401", rec.Code)
	}
}

func TestLogoutRouteClearsSession(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"f@example.com","password":"password123","display_name":"F"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"f@example.com","password":"password123"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}

	rec := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	cleared := sessionCookie(rec)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %+v", cleared)
	}

	// The now-deleted session no longer authorizes logout.
	again := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: c.Name, Value: c.Value})
	if again.Code != http.StatusUnauthorized {
		t.Errorf("logout after logout status = %d, want 401", again.Code)
	}
}

func TestChangePasswordRouteRequiresSession(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/change", `{"current_password":"password123","new_password":"newpassword456"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("change without session status = %d, want 401", rec.Code)
	}
}

func TestChangePasswordRouteStrictDecode(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"g@example.com","password":"password123","display_name":"G"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"g@example.com","password":"password123"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123","new_password":"newpassword456","extra":1}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

func TestChangePasswordRouteWrongCurrent(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"h@example.com","password":"password123","display_name":"H"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"h@example.com","password":"password123"}`)
	c := sessionCookie(login)
	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"wrongcurrent","new_password":"newpassword456"}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong-current status = %d, want 401", rec.Code)
	}
}

func TestChangePasswordRouteHappyPath(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"i@example.com","password":"password123","display_name":"I"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"password123"}`)
	old := sessionCookie(login)
	if old == nil {
		t.Fatal("no session cookie from login")
	}

	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123","new_password":"newpassword456"}`,
		&http.Cookie{Name: old.Name, Value: old.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("change status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	fresh := sessionCookie(rec)
	if fresh == nil || fresh.Value == "" || fresh.Value == old.Value {
		t.Errorf("change did not set a fresh session cookie: old=%v new=%v", old, fresh)
	}

	// The old session cookie is revoked; a gated route now rejects it.
	stale := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: old.Name, Value: old.Value})
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("old cookie after change status = %d, want 401", stale.Code)
	}
	// The fresh session cookie works.
	ok := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: fresh.Name, Value: fresh.Value})
	if ok.Code != http.StatusOK {
		t.Errorf("fresh cookie logout status = %d, want 200", ok.Code)
	}
	// Re-login with the new password succeeds.
	relogin := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"newpassword456"}`)
	if relogin.Code != http.StatusOK {
		t.Errorf("re-login with new password status = %d, want 200", relogin.Code)
	}
}
