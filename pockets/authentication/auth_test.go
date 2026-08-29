package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// --- compile-time seam assertions ---

var (
	// Register conforms to the FS2 mount signature: a method on the built Service.
	_ func(pocket.Mount) error = (&Service{}).Register
	// Service.RequirePrincipal and its named helpers return web.Middleware.
	_ func(...PrincipalOption) web.Middleware = (&Service{}).RequirePrincipal
	_ func() web.Middleware                   = (&Service{}).RequireAccessToken
)

// stubHasher / stubMailer satisfy the required Config ports for the
// happy-path constructor test.
type stubHasher struct{}

func (stubHasher) HashPassword(string) (string, error) { return "x", nil }
func (stubHasher) VerifyPassword(string, string) error { return nil }

type stubMailer struct{}

func (stubMailer) Send(context.Context, email.Message) error { return nil }

// stubSigner satisfies the required TokenSigner for construction tests (none
// drive it — they assert wiring, not token flow).
type stubSigner struct{}

func (stubSigner) Sign(map[string]any, time.Time) (string, error) { return "tok", nil }
func (stubSigner) Verify(string) (map[string]any, error)          { return map[string]any{}, nil }

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

// stubNotifier is a notify.Notifier declaring a fixed kind — the duplicate-kind
// NewService rejection test drives construction only, never delivery.
type stubNotifier struct{ kind string }

func (s stubNotifier) Kind() string                                                 { return s.kind }
func (stubNotifier) Notify(context.Context, identity.Address, notify.Message) error { return nil }

// stubGranter satisfies the invitation Granter for the construction matrix tests —
// none drives a grant, they assert the Granter/InviteCheck wiring rules.
type stubGranter struct{}

func (stubGranter) Grant(context.Context, GrantInput) error { return nil }

// allowInvite is a permissive InviteCheck for the construction matrix tests.
func allowInvite(context.Context, InviteCheckRequest) error { return nil }

// stubInvitations satisfies invitation.InvitationRepository for the construction
// matrix tests: the both-present case drives Create through to the disabled-outbox
// error, proving the subsystem is enabled (not ErrInvitationsDisabled).
type stubInvitations struct{}

func (stubInvitations) Create(_ context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	return inv, nil
}
func (stubInvitations) Get(context.Context, string) (invitation.Invitation, error) {
	return invitation.Invitation{}, nil
}
func (stubInvitations) GetByTokenHash(context.Context, string) (invitation.Invitation, error) {
	return invitation.Invitation{}, nil
}
func (stubInvitations) ListByResource(context.Context, string, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return crud.Page[invitation.Invitation]{}, nil
}
func (stubInvitations) ListBySubject(context.Context, string, string, crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return crud.Page[invitation.Invitation]{}, nil
}
func (stubInvitations) UpdateStatus(context.Context, string, invitation.StatusUpdate) (invitation.Invitation, error) {
	return invitation.Invitation{}, nil
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

// TestNewServiceRequiresTokenSigner proves the signer is required (D3): with a
// Hasher and Mailer wired but no TokenSigner, construction fails loudly.
func TestNewServiceRequiresTokenSigner(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if !errors.Is(err, ErrTokenSignerRequired) {
		t.Errorf("nil TokenSigner: err=%v, want ErrTokenSignerRequired", err)
	}
}

// TestNewServiceDuplicateNotifierKind proves the LOUD duplicate-kind rejection
// (the ErrOAuthReposRequired posture — NOT the OAuth provider map's silent
// last-wins): two notifiers of the same kind → ErrDuplicateNotifierKind; distinct
// kinds construct fine.
func TestNewServiceDuplicateNotifierKind(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}

	dup := base
	dup.Notifiers = []notify.Notifier{stubNotifier{"sms"}, stubNotifier{"sms"}}
	if _, err := NewService(Repositories{}, dup); !errors.Is(err, ErrDuplicateNotifierKind) {
		t.Errorf("duplicate notifier kind: err=%v, want ErrDuplicateNotifierKind", err)
	}

	ok := base
	ok.Notifiers = []notify.Notifier{stubNotifier{"sms"}, stubNotifier{"slack"}}
	if _, err := NewService(Repositories{}, ok); err != nil {
		t.Errorf("distinct notifier kinds: err=%v, want nil", err)
	}
}

func TestNewServiceDefaultsRateLimiter(t *testing.T) {
	// A nil RateLimiter must not error — it defaults to an in-memory limiter.
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff})
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
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, Providers: []oauth.Provider{stubProvider{}}}

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

// TestNewServiceOAuthLinkBaseURL proves the oauth-pending-link plan D5 posture:
// with providers wired an EMPTY OAuthLinkBaseURL constructs successfully (it only
// degrades the email to the bare-token line, with a startup WARN), while a non-empty
// but malformed value fails LOUDLY at construction in every mode. The
// production-HTTPS requirement is exercised directly at the validator level
// (TestValidateOAuthLinkBaseURL), which does not need a production-capable Mailer.
func TestNewServiceOAuthLinkBaseURL(t *testing.T) {
	repos := Repositories{OAuthAccounts: stubOAuthAccounts{}, OAuthStates: stubOAuthStates{}}
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, Providers: []oauth.Provider{stubProvider{}}}

	// Empty is allowed (degrade, don't break boot).
	if _, err := NewService(repos, base); err != nil {
		t.Errorf("providers set, empty OAuthLinkBaseURL: err=%v, want nil", err)
	}
	// A valid absolute https URL is accepted.
	ok := base
	ok.OAuthLinkBaseURL = "https://app.example.com/auth/oauth/link"
	if _, err := NewService(repos, ok); err != nil {
		t.Errorf("valid OAuthLinkBaseURL: err=%v, want nil", err)
	}
	// A malformed value fails in every mode.
	bad := base
	bad.OAuthLinkBaseURL = "not-a-url"
	if _, err := NewService(repos, bad); !errors.Is(err, ErrOAuthLinkURLInvalid) {
		t.Errorf("malformed OAuthLinkBaseURL: err=%v, want ErrOAuthLinkURLInvalid", err)
	}
	// A fragment is rejected — the token owns the fragment.
	frag := base
	frag.OAuthLinkBaseURL = "https://app.example.com/link#already"
	if _, err := NewService(repos, frag); !errors.Is(err, ErrOAuthLinkURLInvalid) {
		t.Errorf("fragment OAuthLinkBaseURL: err=%v, want ErrOAuthLinkURLInvalid", err)
	}
}

// TestNewServiceOAuthOffAllowsNilRepos proves that with no providers (OAuth off)
// the oauth repositories may be nil — deny-by-absence, not a wiring error.
func TestNewServiceOAuthOffAllowsNilRepos(t *testing.T) {
	if _, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}); err != nil {
		t.Errorf("oauth off with nil oauth repos: err=%v, want nil", err)
	}
}

// TestRegisterOAuthDenyByAbsence proves the OAuth routes are absent (404) when no
// provider is wired, at the public Register surface.
func TestRegisterOAuthDenyByAbsence(t *testing.T) {
	h := web.NewWebHandler()
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
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
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}

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

// TestNewServiceInvitationConstructionMatrix proves the D3 construction matrix:
// a Granter with a nil InviteCheck is ErrInviteCheckRequired; both nil leaves
// invitations OFF (Create → ErrInvitationsDisabled); both wired enables them
// (Create runs through to the disabled-outbox error, not ErrInvitationsDisabled).
// An InviteCheck without a Granter is the contradictory ErrInviteCheckWithoutGranter.
func TestNewServiceInvitationConstructionMatrix(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}
	repos := Repositories{Invitations: stubInvitations{}}

	// Granter wired + nil InviteCheck → loud ErrInviteCheckRequired.
	grantNoCheck := base
	grantNoCheck.Granter = stubGranter{}
	if _, err := NewService(repos, grantNoCheck); !errors.Is(err, ErrInviteCheckRequired) {
		t.Errorf("Granter + nil InviteCheck: err=%v, want ErrInviteCheckRequired", err)
	}

	// InviteCheck wired without a Granter → contradictory ErrInviteCheckWithoutGranter.
	checkNoGrant := base
	checkNoGrant.InviteCheck = allowInvite
	if _, err := NewService(Repositories{}, checkNoGrant); !errors.Is(err, ErrInviteCheckWithoutGranter) {
		t.Errorf("InviteCheck + nil Granter: err=%v, want ErrInviteCheckWithoutGranter", err)
	}

	// Both nil → invitations off; Create reports the disabled subsystem.
	off, err := NewService(Repositories{}, base)
	if err != nil {
		t.Fatalf("NewService (invitations off): %v", err)
	}
	if _, err := off.Create(context.Background(), CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", InvitedBy: "inviter"}); !errors.Is(err, ErrInvitationsDisabled) {
		t.Errorf("Create (invitations off): err=%v, want ErrInvitationsDisabled", err)
	}

	// Both wired → invitations enabled: the route surface is mounted (an
	// authenticated invitation route is present, not deny-by-absence 404). The off
	// case above proves the driving Create surface is disabled; this proves the
	// enabled subsystem mounts its routes.
	both := base
	both.Granter = stubGranter{}
	both.InviteCheck = allowInvite
	on, err := NewService(repos, both)
	if err != nil {
		t.Fatalf("NewService (invitations on): %v", err)
	}
	h := web.NewWebHandler()
	if err := on.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register (invitations on): %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/invitations/mine", nil))
	if rec.Code == http.StatusNotFound {
		t.Errorf("invitations on: GET /auth/invitations/mine = 404, want the route mounted (subsystem enabled)")
	}

	// The same route is absent (404) when invitations are off, confirming the probe.
	offHandler := web.NewWebHandler()
	if err := off.Register(pocket.Mount{Router: offHandler}); err != nil {
		t.Fatalf("Register (invitations off): %v", err)
	}
	offRec := httptest.NewRecorder()
	offHandler.ServeHTTP(offRec, httptest.NewRequest("GET", "/auth/invitations/mine", nil))
	if offRec.Code != http.StatusNotFound {
		t.Errorf("invitations off: GET /auth/invitations/mine = %d, want 404 (deny-by-absence)", offRec.Code)
	}
}

// TestInvitationAuthorizedFacadeDelegates proves the facade's policy-carrying
// invitation twins are the AUTHORIZED path, not the trusted one: with invitations
// off both report ErrInvitationsDisabled, and with the subsystem wired each reaches
// the host InviteCheck carrying the caller's principal — CreateAuthorized after
// preparation (the recorded identifier is normalized, the action is InviteCreate)
// and ListByResourceAuthorized with the empty invitee context. A denial propagates
// unwrapped, which the check-free Service.Create/ListByResource could never do.
func TestInvitationAuthorizedFacadeDelegates(t *testing.T) {
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff}
	caller := identity.Principal{Type: "user", ID: "inviter-1"}
	ctx := context.Background()

	off, err := NewService(Repositories{}, base)
	if err != nil {
		t.Fatalf("NewService (invitations off): %v", err)
	}
	if _, err := off.CreateAuthorized(ctx, caller, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", InvitedBy: "inviter-1"}); !errors.Is(err, ErrInvitationsDisabled) {
		t.Errorf("CreateAuthorized (invitations off): err=%v, want ErrInvitationsDisabled", err)
	}
	if _, err := off.ListByResourceAuthorized(ctx, caller, "project", "p1", crud.ListRequest{}); !errors.Is(err, ErrInvitationsDisabled) {
		t.Errorf("ListByResourceAuthorized (invitations off): err=%v, want ErrInvitationsDisabled", err)
	}

	denied := errors.New("host policy refused")
	var posed []InviteCheckRequest
	on := base
	on.Granter = stubGranter{}
	on.InviteCheck = func(_ context.Context, req InviteCheckRequest) error {
		posed = append(posed, req)
		return denied
	}
	// Identifiers backs the invitee lookup prepareCreate runs before the policy is
	// posed; it holds no rows, so every invitee resolves to no existing subject.
	svc, err := NewService(Repositories{Invitations: stubInvitations{}, Identifiers: &memIdentifierRepo{}}, on)
	if err != nil {
		t.Fatalf("NewService (invitations on): %v", err)
	}

	if _, err := svc.CreateAuthorized(ctx, caller, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "Invitee@X.com", InvitedBy: "inviter-1"}); !errors.Is(err, denied) {
		t.Fatalf("CreateAuthorized: err=%v, want the host denial", err)
	}
	if len(posed) != 1 {
		t.Fatalf("CreateAuthorized posed InviteCheck %d times, want 1", len(posed))
	}
	create := posed[0]
	if create.Principal != caller {
		t.Errorf("CreateAuthorized principal = %+v, want %+v", create.Principal, caller)
	}
	if create.Action != InviteCreate {
		t.Errorf("CreateAuthorized action = %v, want InviteCreate", create.Action)
	}
	if create.Identifier != "invitee@x.com" {
		t.Errorf("CreateAuthorized identifier = %q, want the normalized %q", create.Identifier, "invitee@x.com")
	}
	if create.Relation != "member" || create.ResourceType != "project" || create.ResourceID != "p1" {
		t.Errorf("CreateAuthorized resource/relation = %q/%q/%q, want project/p1/member", create.ResourceType, create.ResourceID, create.Relation)
	}

	posed = nil
	if _, err := svc.ListByResourceAuthorized(ctx, caller, "project", "p1", crud.ListRequest{}); !errors.Is(err, denied) {
		t.Fatalf("ListByResourceAuthorized: err=%v, want the host denial", err)
	}
	if len(posed) != 1 {
		t.Fatalf("ListByResourceAuthorized posed InviteCheck %d times, want 1", len(posed))
	}
	list := posed[0]
	if list.Principal != caller {
		t.Errorf("ListByResourceAuthorized principal = %+v, want %+v", list.Principal, caller)
	}
	if list.Action != InviteList {
		t.Errorf("ListByResourceAuthorized action = %v, want InviteList", list.Action)
	}
	if list.Relation != "" || list.Identifier != "" || list.IdentifierKind != "" || list.ResolvedSubjectID != "" || len(list.Metadata) != 0 {
		t.Errorf("ListByResourceAuthorized carried invitee context: %+v, want it empty", list)
	}
}

// TestRegisterMachineDenyByAbsence proves the machine lifecycle routes are absent
// (404) when no machine repos are wired, at the public Register surface.
func TestRegisterMachineDenyByAbsence(t *testing.T) {
	h := web.NewWebHandler()
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/service-accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("service-accounts (no machine repos) status = %d, want 404", rec.Code)
	}
}

// TestRegisterMachineNoGateDenyByAbsence proves the machine lifecycle routes are
// absent (404) when the repos are wired but no Config.MachineRoutesGate names a
// policy: the pocket never guesses one, so it mounts nothing (D1).
func TestRegisterMachineNoGateDenyByAbsence(t *testing.T) {
	h := web.NewWebHandler()
	repos := Repositories{ServiceAccounts: stubServiceAccounts{}, APIKeys: stubAPIKeys{}}
	svc, err := NewService(repos, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/service-accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("service-accounts (repos wired, no gate) status = %d, want 404", rec.Code)
	}
}

// TestRegisterMachineMountsRoutes proves the machine routes ARE mounted and
// identity-gated (401 without a credential — the host's gate never runs) when
// both machine repos are wired AND a gate is named.
func TestRegisterMachineMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	repos := Repositories{ServiceAccounts: stubServiceAccounts{}, APIKeys: stubAPIKeys{}}
	gateRan := false
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gateRan = true
			next.ServeHTTP(w, r)
		})
	}
	svc, err := NewService(repos, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, MachineRoutesGate: gate})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := httptest.NewRequest("GET", "/auth/service-accounts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("service-accounts status = %d, want 401 (route mounted + gated)", rec.Code)
	}
	if gateRan {
		t.Error("the host gate ran for an unauthenticated request; the authenticator must be outermost")
	}
}

// TestNewServiceMachineGateWithoutRepos proves the contradictory wiring fails
// LOUDLY: a gate with no machine subsystem is a policy that can never run.
func TestNewServiceMachineGateWithoutRepos(t *testing.T) {
	gate := func(next http.Handler) http.Handler { return next }
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, MachineRoutesGate: gate})
	if !errors.Is(err, ErrMachineRoutesGateWithoutRepos) {
		t.Fatalf("gate without machine repos: err=%v, want ErrMachineRoutesGateWithoutRepos", err)
	}
	// One repository is not enough either — the pair check fires first there, so
	// pair BOTH with a gate to isolate this error.
	_, err = NewService(Repositories{ServiceAccounts: stubServiceAccounts{}}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, MachineRoutesGate: gate})
	if !errors.Is(err, ErrMachineReposRequired) {
		t.Fatalf("half-wired machine repos: err=%v, want ErrMachineReposRequired", err)
	}
}

// TestNewServiceWarnsOnMachineReposWithoutGate proves an upgrading host learns
// the deny-by-absence posture at BOOT rather than from production 404s.
func TestNewServiceWarnsOnMachineReposWithoutGate(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	repos := Repositories{ServiceAccounts: stubServiceAccounts{}, APIKeys: stubAPIKeys{}}
	base := Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff, Logger: log}
	if _, err := NewService(repos, base); err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Config.MachineRoutesGate is unset")) {
		t.Errorf("expected the machine-gate posture WARN, got: %s", buf.String())
	}

	buf.Reset()
	gated := base
	gated.MachineRoutesGate = func(next http.Handler) http.Handler { return next }
	if _, err := NewService(repos, gated); err != nil {
		t.Fatalf("NewService (gated): %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("Config.MachineRoutesGate is unset")) {
		t.Errorf("a gated host must not see the posture WARN, got: %s", buf.String())
	}
}

func TestRegisterMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: RuntimeModeDevelopment, DeliveryMode: DeliveryModeOff})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// The mounted password/change route exists and is credential-management gated
	// (401 without a credential), proving the routes were registered onto the
	// mount's router.
	req := httptest.NewRequest("POST", "/auth/password/change", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("password/change status = %d, want 401 (route mounted + gated)", rec.Code)
	}
}

// --- Config.BundledRouteAuth: an override is EXPLICIT, never "unset" ---

// liveServiceAccounts / liveAPIKeys are working machine repositories (the
// stub* pair above returns zero values, which cannot carry a real key). They back
// the bundled-route posture cases, where an actual API key must resolve.
type liveServiceAccounts struct {
	mu sync.Mutex
	m  map[string]serviceaccount.ServiceAccount
}

func (r *liveServiceAccounts) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sa.ID] = sa
	return sa, nil
}
func (r *liveServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sa, ok := r.m[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	return sa, nil
}
func (r *liveServiceAccounts) List(context.Context, crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return crud.Page[serviceaccount.ServiceAccount]{Items: []serviceaccount.ServiceAccount{}}, nil
}
func (r *liveServiceAccounts) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[id] = sa
	return sa, nil
}
func (r *liveServiceAccounts) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
	return nil
}

type liveAPIKeys struct {
	mu sync.Mutex
	m  map[string]apikey.APIKey
}

func (r *liveAPIKeys) Create(_ context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[k.ID] = k
	return k, nil
}
func (r *liveAPIKeys) GetByHash(_ context.Context, hash string) (apikey.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.m {
		if k.KeyHash == hash {
			return k, nil
		}
	}
	return apikey.APIKey{}, sdk.ErrNotFound
}
func (r *liveAPIKeys) ListByServiceAccount(context.Context, string, crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return crud.Page[apikey.APIKey]{Items: []apikey.APIKey{}}, nil
}
func (r *liveAPIKeys) Revoke(_ context.Context, id string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.m[id]
	if !ok {
		return sdk.ErrNotFound
	}
	k.RevokedAt = at
	r.m[id] = k
	return nil
}
func (r *liveAPIKeys) TouchLastUsed(context.Context, string, time.Time) error { return nil }

// bundledRouteHost is a mounted host plus the key its machine subsystem minted
// and a counter for the host's machine gate.
type bundledRouteHost struct {
	h        http.Handler
	rawKey   string
	gateRuns *int
}

// newBundledRouteHost builds a Service with a working machine subsystem, applies
// bundled, mounts the routes, and mints one self-acting service-account key.
func newBundledRouteHost(t *testing.T, bundled BundledRouteAuthentication) bundledRouteHost {
	t.Helper()
	runs := 0
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			runs++
			next.ServeHTTP(w, r)
		})
	}
	svc, err := NewService(
		Repositories{
			ServiceAccounts: &liveServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
			APIKeys:         &liveAPIKeys{m: map[string]apikey.APIKey{}},
		},
		Config{
			Hasher:            stubHasher{},
			Mailer:            stubMailer{},
			TokenSigner:       stubSigner{},
			RuntimeMode:       RuntimeModeDevelopment,
			DeliveryMode:      DeliveryModeOff,
			MachineRoutesGate: gate,
			BundledRouteAuth:  bundled,
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h := web.NewWebHandler()
	if err := svc.Register(pocket.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx := context.Background()
	sa, err := svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	return bundledRouteHost{h: h, rawKey: raw, gateRuns: &runs}
}

// keyRequest drives one route with the host's API key as its bearer credential.
func (b bundledRouteHost) keyRequest(method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+b.rawKey)
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, r)
	return rec
}

// TestBundledRouteAuthOverrideIsExplicitNotUnset pins why RoutePrincipalStrategy
// is opaque: a zero-argument PrincipalStrategy() is a CONFIGURED posture meaning
// RequirePrincipal() — every wired credential, both transports, stateless — and
// not "leave the audited default alone". The session-security default refuses
// every API key; the explicit primitive admits one, and the caller reaches the
// handler's own missing-receipt answer.
func TestBundledRouteAuthOverrideIsExplicitNotUnset(t *testing.T) {
	unset := newBundledRouteHost(t, BundledRouteAuthentication{})
	if rec := unset.keyRequest("GET", "/auth/delivery/status"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("audited default: api key on /auth/delivery/status = %d, want 401; body=%s", rec.Code, rec.Body)
	}

	explicit := newBundledRouteHost(t, BundledRouteAuthentication{SessionSecurityReads: PrincipalStrategy()})
	rec := explicit.keyRequest("GET", "/auth/delivery/status")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PrincipalStrategy(): api key on /auth/delivery/status = %d, want 400 (the handler's own missing-receipt answer); body=%s", rec.Code, rec.Body)
	}

	// Every slot the host did NOT configure keeps its audited default: the machine
	// lifecycle still refuses the same key, and the host's gate never runs for it.
	if rec := explicit.keyRequest("GET", "/auth/service-accounts"); rec.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured MachineLifecycle: api key = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if *explicit.gateRuns != 0 {
		t.Errorf("the host machine gate ran %d times for a refused credential, want 0", *explicit.gateRuns)
	}
	if rec := explicit.keyRequest("POST", "/auth/password/change"); rec.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured CredentialManagement: api key = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	// SessionHydration's default admits a key; a SELF-acting one then fails the
	// handler's own CurrentUser requirement — unchanged by the override above.
	if rec := explicit.keyRequest("GET", "/auth/me"); rec.Code != http.StatusUnauthorized {
		t.Errorf("unconfigured SessionHydration: self-acting key = %d, want 401; body=%s", rec.Code, rec.Body)
	}
}
