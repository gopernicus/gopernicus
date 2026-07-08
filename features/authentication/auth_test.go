package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/logic/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// --- compile-time seam assertions ---

var (
	// Register conforms to the FS2 mount signature: a method on the built Service.
	_ func(feature.Mount) error = (&Service{}).Register
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

// stub machine repos satisfy the machine ports for the both-or-neither
// construction tests. None is driven — the tests assert construction/routing.
type stubServiceAccounts struct{}

func (stubServiceAccounts) Create(context.Context, serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	return serviceaccount.ServiceAccount{}, nil
}
func (stubServiceAccounts) Get(context.Context, string) (serviceaccount.ServiceAccount, error) {
	return serviceaccount.ServiceAccount{}, nil
}
func (stubServiceAccounts) List(context.Context, crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return crud.Page[serviceaccount.ServiceAccount]{}, nil
}
func (stubServiceAccounts) Update(context.Context, string, serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	return serviceaccount.ServiceAccount{}, nil
}
func (stubServiceAccounts) Delete(context.Context, string) error { return nil }

type stubAPIKeys struct{}

func (stubAPIKeys) Create(context.Context, apikey.APIKey) (apikey.APIKey, error) {
	return apikey.APIKey{}, nil
}
func (stubAPIKeys) GetByHash(context.Context, string) (apikey.APIKey, error) {
	return apikey.APIKey{}, nil
}
func (stubAPIKeys) ListByServiceAccount(context.Context, string, crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return crud.Page[apikey.APIKey]{}, nil
}
func (stubAPIKeys) Revoke(context.Context, string, time.Time) error        { return nil }
func (stubAPIKeys) TouchLastUsed(context.Context, string, time.Time) error { return nil }

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
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(feature.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/oauth/github/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("oauth start (no providers) status = %d, want 404", rec.Code)
	}
}

// TestNewServiceMachinePartialWiring proves the both-or-neither rule (cut
// refinement 5): one machine repo without the other → ErrMachineReposRequired;
// both wired → ok; neither → ok (subsystem off).
func TestNewServiceMachinePartialWiring(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}}

	if _, err := NewService(Repositories{ServiceAccounts: stubServiceAccounts{}}, base); !errors.Is(err, ErrMachineReposRequired) {
		t.Errorf("service accounts only: err=%v, want ErrMachineReposRequired", err)
	}
	if _, err := NewService(Repositories{APIKeys: stubAPIKeys{}}, base); !errors.Is(err, ErrMachineReposRequired) {
		t.Errorf("api keys only: err=%v, want ErrMachineReposRequired", err)
	}
	if _, err := NewService(Repositories{ServiceAccounts: stubServiceAccounts{}, APIKeys: stubAPIKeys{}}, base); err != nil {
		t.Errorf("both machine repos wired: err=%v, want nil", err)
	}
	if _, err := NewService(Repositories{}, base); err != nil {
		t.Errorf("no machine repos (subsystem off): err=%v, want nil", err)
	}
}

// TestRegisterMachineDenyByAbsence proves the machine lifecycle routes are absent
// (404) when no machine repos are wired, at the public Register surface.
func TestRegisterMachineDenyByAbsence(t *testing.T) {
	h := web.NewWebHandler()
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(feature.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/service-accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("service-accounts (no machine repos) status = %d, want 404", rec.Code)
	}
}

// TestRegisterMachineMountsRoutes proves the machine routes ARE mounted and
// session-gated (401 without one) when both machine repos are wired.
func TestRegisterMachineMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	repos := Repositories{ServiceAccounts: stubServiceAccounts{}, APIKeys: stubAPIKeys{}}
	svc, err := NewService(repos, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(feature.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/service-accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("service-accounts status = %d, want 401 (route mounted + gated)", rec.Code)
	}
}

func TestRegisterMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(feature.Mount{Router: h}); err != nil {
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
