package authentication

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Browser login-path tests (#12). Config.BrowserLoginPath configures ONLY the browser
// identity gates. A non-empty value must validate as a safe root-relative path so a
// gate can never be pointed off-site; empty defaults to "/auth/login".

func browserBaseConfig() Config {
	return Config{
		Hasher:       stubHasher{},
		Mailer:       stubMailer{},
		TokenSigner:  stubSigner{},
		RuntimeMode:  RuntimeModeDevelopment,
		DeliveryMode: DeliveryModeOff,
	}
}

// TestBrowserLoginPathConstructionMatrix proves an empty path defaults, a safe
// root-relative override constructs, and every open-redirect vector fails LOUDLY with
// ErrBrowserLoginPathInvalid — both as a config value.
func TestBrowserLoginPathConstructionMatrix(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"empty defaults", "", nil},
		{"safe root-relative", "/signin", nil},
		{"safe with query", "/signin?next=1", nil},
		{"protocol-relative", "//evil.com", ErrBrowserLoginPathInvalid},
		{"absolute scheme", "https://evil.com", ErrBrowserLoginPathInvalid},
		{"backslash", "/\\evil.com", ErrBrowserLoginPathInvalid},
		{"control character", "/a\x00b", ErrBrowserLoginPathInvalid},
		{"not leading slash", "signin", ErrBrowserLoginPathInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := browserBaseConfig()
			cfg.BrowserLoginPath = tt.path
			_, err := NewService(Repositories{}, cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("NewService: err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewService: err=%v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestBrowserLoginPathOverrideReachesGate proves the validated override is threaded
// into the browser gates: a denied GET 303s to the configured path (with a validated
// return_to), not the default.
func TestBrowserLoginPathOverrideReachesGate(t *testing.T) {
	cfg := browserBaseConfig()
	cfg.BrowserLoginPath = "/signin"
	svc, err := NewService(Repositories{}, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	svc.RequirePrincipalBrowser(next).ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("denied browser GET = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/signin?return_to=%2Fadmin" {
		t.Errorf("Location = %q, want /signin?return_to=%%2Fadmin", loc)
	}
}
