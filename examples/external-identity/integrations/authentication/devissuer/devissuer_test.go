package devissuer

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func TestNewRefusesOutsideDevelopment(t *testing.T) {
	for _, mode := range []environment.Mode{environment.ModeProduction, "", "staging"} {
		i, err := New("http://localhost:8080", mode)
		if i != nil || !errors.Is(err, ErrNotDevelopment) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("New(mode %q) = %v, %v; want nil, ErrNotDevelopment", mode, i, err)
		}
	}
}

func TestNewRejectsAnOriginWithoutScheme(t *testing.T) {
	if _, err := New("localhost:8080", environment.ModeDevelopment); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New(no scheme) error = %v; want ErrInvalidInput", err)
	}
}

func TestTokenCarriesRequestedClaims(t *testing.T) {
	i := newIssuer(t)
	rec := httptest.NewRecorder()
	i.TokenHandler()(rec, httptest.NewRequest(http.MethodPost, TokenPath,
		strings.NewReader(`{"sub":"u1","email":"a@x.test","email_verified":false,"amr":["pwd","mfa"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var body struct{ Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	claims := payload(t, body.Token)
	if claims["iss"] != i.Issuer() || claims["sub"] != "u1" || claims["aud"] != Audience || claims["email_verified"] != false {
		t.Fatalf("claims = %v", claims)
	}
	if amr, _ := claims["amr"].([]any); len(amr) != 2 || amr[1] != "mfa" {
		t.Fatalf("amr = %v", claims["amr"])
	}
	if _, ok := claims["auth_time"]; !ok {
		t.Fatal("auth_time missing")
	}
}

func TestTokenIssuerOverride(t *testing.T) {
	i := newIssuer(t)
	rec := httptest.NewRecorder()
	i.TokenHandler()(rec, httptest.NewRequest(http.MethodPost, TokenPath, strings.NewReader(`{"sub":"u1","iss":"http://elsewhere.test"}`)))
	var body struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got := payload(t, body.Token)["iss"]; got != "http://elsewhere.test" {
		t.Fatalf("iss = %v", got)
	}
}

func TestSessionSetsTheProviderCookie(t *testing.T) {
	i := newIssuer(t)
	rec := httptest.NewRecorder()
	i.SessionHandler()(rec, httptest.NewRequest(http.MethodPost, SessionPath, strings.NewReader(`{"sub":"u1"}`)))
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Name != CookieName {
		t.Fatalf("status %d cookies %v", rec.Code, cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Fatalf("cookie attributes = %+v; want HttpOnly, Lax, not Secure on http", c)
	}
	if got := payload(t, c.Value)["iss"]; got != i.SessionIssuer() {
		t.Fatalf("session iss = %v; want %s", got, i.SessionIssuer())
	}
}

func TestMintRejectsBadBodies(t *testing.T) {
	i := newIssuer(t)
	for _, body := range []string{``, `{}`, `{"sub":"  "}`, `{"sub":"u1","role":"admin"}`} {
		rec := httptest.NewRecorder()
		i.TokenHandler()(rec, httptest.NewRequest(http.MethodPost, TokenPath, strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status %d; want 400", body, rec.Code)
		}
	}
}

func TestKeysHandlerServesTheJWKS(t *testing.T) {
	i := newIssuer(t)
	rec := httptest.NewRecorder()
	i.KeysHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, KeysPath, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"keys"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if i.JWKSURL() != "http://localhost:8080"+KeysPath {
		t.Fatalf("JWKSURL = %s", i.JWKSURL())
	}
}

func newIssuer(t *testing.T) *Issuer {
	t.Helper()
	i, err := New("http://localhost:8080/", environment.ModeDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func payload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not a JWS", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}
