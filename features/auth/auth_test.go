package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// --- compile-time seam assertions ---

var (
	// Register conforms to the feature contract's registration signature.
	_ func(feature.Mount, Repositories, Config) error = Register
	// Service.RequireUser is a web.Middleware via its method value.
	_ web.Middleware = (&Service{}).RequireUser
)

// stubHasher / stubMailer satisfy the required Config ports for the
// happy-path constructor test.
type stubHasher struct{}

func (stubHasher) HashPassword(string) (string, error) { return "x", nil }
func (stubHasher) VerifyPassword(string, string) error { return nil }

type stubMailer struct{}

func (stubMailer) Send(context.Context, email.Message) error { return nil }

// stubProvider / stub oauth repos satisfy the OAuth ports for the partial-wiring
// construction tests. None is driven — the tests assert construction, not flow.
type stubProvider struct{}

func (stubProvider) Name() string                                 { return "google" }
func (stubProvider) SupportsOIDC() bool                           { return false }
func (stubProvider) TrustEmailVerification() bool                 { return true }
func (stubProvider) GetAuthorizationURL(_, _, _, _ string) string { return "" }
func (stubProvider) ExchangeCode(context.Context, string, string, string) (*oauth.TokenResponse, error) {
	return nil, nil
}
func (stubProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) { return nil, nil }
func (stubProvider) ValidateIDToken(context.Context, string, string) (*oauth.IDTokenClaims, error) {
	return nil, nil
}
func (stubProvider) RefreshToken(context.Context, string) (*oauth.TokenResponse, error) {
	return nil, nil
}

type stubOAuthAccounts struct{}

func (stubOAuthAccounts) Create(context.Context, oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	return oauthaccount.OAuthAccount{}, nil
}
func (stubOAuthAccounts) GetByProvider(context.Context, string, string) (oauthaccount.OAuthAccount, error) {
	return oauthaccount.OAuthAccount{}, nil
}
func (stubOAuthAccounts) ListByUser(context.Context, string) ([]oauthaccount.OAuthAccount, error) {
	return nil, nil
}
func (stubOAuthAccounts) Delete(context.Context, string, string) error { return nil }

type stubOAuthStates struct{}

func (stubOAuthStates) Create(context.Context, oauthstate.State) (oauthstate.State, error) {
	return oauthstate.State{}, nil
}
func (stubOAuthStates) Consume(context.Context, string) (oauthstate.State, error) {
	return oauthstate.State{}, nil
}

func TestNewServiceRequiresHasher(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Mailer: stubMailer{}})
	if !errors.Is(err, ErrHasherRequired) {
		t.Errorf("nil Hasher: err=%v, want ErrHasherRequired", err)
	}
}

func TestNewServiceRequiresMailer(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}})
	if !errors.Is(err, ErrMailerRequired) {
		t.Errorf("nil Mailer: err=%v, want ErrMailerRequired", err)
	}
}

func TestNewServiceDefaultsRateLimiter(t *testing.T) {
	// A nil RateLimiter must not error — it defaults to an in-memory limiter.
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil Service")
	}
}

func TestRegisterRequiresHasher(t *testing.T) {
	err := Register(feature.Mount{}, Repositories{}, Config{Mailer: stubMailer{}})
	if !errors.Is(err, ErrHasherRequired) {
		t.Errorf("Register nil Hasher: err=%v, want ErrHasherRequired", err)
	}
}

// TestNewServiceOAuthPartialWiring proves the loud partial-wiring error: providers
// set but either oauth repository nil → ErrOAuthReposRequired; both wired → ok.
func TestNewServiceOAuthPartialWiring(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, Providers: []oauth.Provider{stubProvider{}}}

	if _, err := NewService(Repositories{}, base); !errors.Is(err, ErrOAuthReposRequired) {
		t.Errorf("providers set, both repos nil: err=%v, want ErrOAuthReposRequired", err)
	}
	if _, err := NewService(Repositories{OAuthAccounts: stubOAuthAccounts{}}, base); !errors.Is(err, ErrOAuthReposRequired) {
		t.Errorf("providers set, states nil: err=%v, want ErrOAuthReposRequired", err)
	}
	if _, err := NewService(Repositories{OAuthStates: stubOAuthStates{}}, base); !errors.Is(err, ErrOAuthReposRequired) {
		t.Errorf("providers set, accounts nil: err=%v, want ErrOAuthReposRequired", err)
	}
	if _, err := NewService(Repositories{OAuthAccounts: stubOAuthAccounts{}, OAuthStates: stubOAuthStates{}}, base); err != nil {
		t.Errorf("providers set, both repos wired: err=%v, want nil", err)
	}
}

// TestNewServiceOAuthOffAllowsNilRepos proves that with no providers (OAuth off)
// the oauth repositories may be nil — deny-by-absence, not a wiring error.
func TestNewServiceOAuthOffAllowsNilRepos(t *testing.T) {
	if _, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}}); err != nil {
		t.Errorf("oauth off with nil oauth repos: err=%v, want nil", err)
	}
}

// TestRegisterOAuthDenyByAbsence proves the OAuth routes are absent (404) when no
// provider is wired, at the public Register surface.
func TestRegisterOAuthDenyByAbsence(t *testing.T) {
	h := web.NewWebHandler()
	if err := Register(feature.Mount{Router: h}, Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/oauth/github/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("oauth start (no providers) status = %d, want 404", rec.Code)
	}
}

func TestRegisterMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	err := Register(feature.Mount{Router: h}, Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// The mounted logout route exists and is session-gated (401 without one),
	// proving the routes were registered onto the mount's router.
	req := httptest.NewRequest("POST", "/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("logout status = %d, want 401 (route mounted + gated)", rec.Code)
	}
}
