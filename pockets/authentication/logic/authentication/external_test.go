package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	extIssuerA = "https://issuer-a.example.test"
	extIssuerB = "https://issuer-b.example.test"
)

// extToken builds a JWT-shaped credential. The fake provider looks it up
// verbatim; only the issuer peek reads the payload.
func extToken(iss, sub string) string {
	body, _ := json.Marshal(map[string]any{"iss": iss, "sub": sub})
	return "e30." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

type extProvider struct {
	issuer     string
	identities map[string]sdkauth.Identity
	err        error
	calls      atomic.Int64
}

func (p *extProvider) Issuer() string { return p.issuer }

func (p *extProvider) Authenticate(_ context.Context, cred sdkauth.Credential) (sdkauth.Identity, error) {
	p.calls.Add(1)
	if p.err != nil {
		return sdkauth.Identity{}, p.err
	}
	id, ok := p.identities[cred.Raw]
	if !ok {
		return sdkauth.Identity{}, sdkauth.ErrInvalidCredential
	}
	return id, nil
}

type extRevocation struct {
	mu    sync.Mutex
	facts map[string]sdkauth.RevocationFacts
	err   error
	calls atomic.Int64
	gate  chan struct{}
}

func (r *extRevocation) CheckRevocation(_ context.Context, _, subject string) (sdkauth.RevocationFacts, error) {
	r.calls.Add(1)
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return sdkauth.RevocationFacts{}, r.err
	}
	f, ok := r.facts[subject]
	if !ok {
		return sdkauth.RevocationFacts{}, sdk.ErrNotFound
	}
	return f, nil
}

func (r *extRevocation) set(subject string, f sdkauth.RevocationFacts, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.facts == nil {
		r.facts = map[string]sdkauth.RevocationFacts{}
	}
	r.facts[subject] = f
	r.err = err
}

// extStore is one in-memory backing for the three engine ports.
type extStore struct {
	mu             sync.Mutex
	users          map[string]user.User
	idents         []identifier.Identifier
	subjects       map[string]externalsubject.ExternalSubject
	dropExternal   bool
	getErr         error
	userGets       atomic.Int64
	provisionCalls atomic.Int64
	provisioned    []user.InitialCredentials
	seq            int
}

func newExtStore() *extStore {
	return &extStore{users: map[string]user.User{}, subjects: map[string]externalsubject.ExternalSubject{}}
}

type extUsers struct{ *extStore }
type extIdentifiers struct{ *extStore }
type extSubjects struct{ *extStore }

func (s extUsers) Provision(_ context.Context, u user.User, ident identifier.Identifier, creds user.InitialCredentials) (user.User, identifier.Identifier, error) {
	s.provisionCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provisioned = append(s.provisioned, creds)
	if creds.External != nil {
		if _, taken := s.subjects[creds.External.Provider+"|"+creds.External.Subject]; taken {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	s.seq++
	u.ID = "user-" + string(rune('0'+s.seq))
	s.users[u.ID] = u
	ident.UserID = u.ID
	s.idents = append(s.idents, ident)
	if creds.External != nil && !s.dropExternal {
		row := *creds.External
		row.UserID = u.ID
		s.subjects[row.Provider+"|"+row.Subject] = row
	}
	return u, ident, nil
}

func (s extUsers) CreateWithPrimaryIdentifier(context.Context, user.User, identifier.Identifier) (user.User, identifier.Identifier, error) {
	return user.User{}, identifier.Identifier{}, errors.New("unused")
}

func (s extUsers) Get(_ context.Context, id string) (user.User, error) {
	s.userGets.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return user.User{}, s.getErr
	}
	u, ok := s.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

func (s extUsers) Update(context.Context, string, user.User) (user.User, error) {
	return user.User{}, errors.New("unused")
}

func (s extIdentifiers) Get(context.Context, string) (identifier.Identifier, error) {
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (s extIdentifiers) lookup(kind, value string, login bool) (identifier.Identifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.idents {
		if string(it.Kind) == kind && it.NormalizedValue == value && it.Active() && ((login && it.LoginEnabled) || (!login && it.RecoveryEnabled)) {
			return it, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (s extIdentifiers) GetLogin(_ context.Context, kind, value string) (identifier.Identifier, error) {
	return s.lookup(kind, value, true)
}

func (s extIdentifiers) GetRecovery(_ context.Context, kind, value string) (identifier.Identifier, error) {
	return s.lookup(kind, value, false)
}

func (s extIdentifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []identifier.Identifier
	for _, it := range s.idents {
		if it.UserID == userID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s extIdentifiers) ApplyVerifiedChange(context.Context, identifier.ApplyVerifiedChangeInput, int64, time.Time) (identifier.Identifier, error) {
	return identifier.Identifier{}, errors.New("unused")
}

func (s extSubjects) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.subjects[provider+"|"+subject]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return row, nil
}

func (s extSubjects) Link(_ context.Context, row externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.subjects[row.Provider+"|"+row.Subject]; taken {
		return externalsubject.ExternalSubject{}, sdk.ErrAlreadyExists
	}
	s.subjects[row.Provider+"|"+row.Subject] = row
	return row, nil
}

func (s extSubjects) ListByUser(context.Context, string) ([]externalsubject.ExternalSubject, error) {
	return nil, nil
}

func (s extSubjects) Unlink(context.Context, string, string) error { return sdk.ErrNotFound }

// addUser seeds an existing local user mapped to (provider, subject).
func (s *extStore) addUser(id, provider, subject string, status user.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[id] = user.User{ID: id, Status: status}
	if subject != "" {
		s.subjects[provider+"|"+subject] = externalsubject.ExternalSubject{Provider: provider, Subject: subject, UserID: id}
	}
}

type extInvitations struct {
	mu    sync.Mutex
	calls []string
}

func (i *extInvitations) ResolveInvitations(_ context.Context, _, email, _, subjectID string) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, email+"|"+subjectID)
	return 1, nil
}

func (i *extInvitations) ResolvePendingInvitations(context.Context, string, string, string, string) (int, error) {
	return 0, nil
}

type failingCache struct{ cacher.Storer }

func (failingCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("cache down")
}

func (failingCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("cache down")
}

type extClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *extClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *extClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type extHarness struct {
	engine      *ExternalAuthenticator
	store       *extStore
	providerA   *extProvider
	providerB   *extProvider
	revocation  *extRevocation
	events      *spySecurityEvents
	invitations *extInvitations
	clock       *extClock
}

func newExtHarness(t *testing.T, configure func(*ExternalConfig)) *extHarness {
	t.Helper()
	h := &extHarness{
		store:       newExtStore(),
		providerA:   &extProvider{issuer: extIssuerA, identities: map[string]sdkauth.Identity{}},
		providerB:   &extProvider{issuer: extIssuerB, identities: map[string]sdkauth.Identity{}},
		revocation:  &extRevocation{},
		events:      newSpySecurityEvents(),
		invitations: &extInvitations{},
		clock:       &extClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)},
	}
	cfg := ExternalConfig{Providers: []ExternalProvider{
		{Name: "alpha", Provider: h.providerA, Revocation: h.revocation, ProvisionOnFirstVerification: true},
		{Name: "beta", Provider: h.providerB},
	}}
	if configure != nil {
		configure(&cfg)
	}
	engine, err := NewExternalAuthenticator(ExternalRepositories{
		Users:            extUsers{h.store},
		Identifiers:      extIdentifiers{h.store},
		ExternalSubjects: extSubjects{h.store},
		SecurityEvents:   h.events,
		Invitations:      h.invitations,
	}, cfg, environment.ModeDevelopment, WithExternalClock(h.clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	h.engine = engine
	return h
}

// token registers an identity on p and returns its credential.
func (h *extHarness) token(p *extProvider, id sdkauth.Identity) string {
	id.Issuer = p.issuer
	if id.AuthenticatedAt.IsZero() {
		id.AuthenticatedAt = h.clock.Now().Add(-time.Minute)
	}
	raw := extToken(p.issuer, id.Subject)
	p.identities[raw] = id
	return raw
}

func (h *extHarness) eventsOf(eventType string) []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, e := range h.events.recorded() {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func TestExternalUnknownIssuerDeniesWithoutCallingAnyProvider(t *testing.T) {
	h := newExtHarness(t, nil)
	for _, raw := range []string{extToken("https://unwired.example.test", "s1"), "not-a-jwt", extToken("", "s1")} {
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("Verify(%q) = %v, want unauthorized", raw, err)
		}
	}
	if h.providerA.calls.Load()+h.providerB.calls.Load() != 0 {
		t.Fatal("an unwired issuer reached a provider")
	}
	if _, ok := h.engine.ClassifyBearer(extToken("https://unwired.example.test", "s1")); ok {
		t.Fatal("unwired issuer classified")
	}
	if kind, ok := h.engine.ClassifyBearer(extToken(extIssuerA, "s1")); !ok || kind != CredentialExternalToken {
		t.Fatal("wired issuer not classified as an external token")
	}
}

func TestExternalProviderFailureNeverFallsThrough(t *testing.T) {
	for name, providerErr := range map[string]error{
		"invalid":     sdkauth.ErrInvalidCredential,
		"unavailable": &sdkauth.UnavailableError{Kind: sdkauth.UnavailableNetwork},
	} {
		t.Run(name, func(t *testing.T) {
			h := newExtHarness(t, nil)
			raw := h.token(h.providerB, sdkauth.Identity{Subject: "s1", Email: "a@example.test"})
			h.providerA.identities[raw] = sdkauth.Identity{Issuer: extIssuerA, Subject: "s1"}
			h.providerB.err = providerErr
			_, _, err := h.engine.Verify(context.Background(), raw, TransportHeader)
			if name == "unavailable" && !errors.Is(err, sdk.ErrUnavailable) {
				t.Fatalf("outage = %v, want unavailable", err)
			}
			if name == "invalid" && !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("rejection = %v, want unauthorized", err)
			}
			if h.providerA.calls.Load() != 0 || h.providerB.calls.Load() != 1 {
				t.Fatalf("calls A=%d B=%d, want exactly one call to the routed provider", h.providerA.calls.Load(), h.providerB.calls.Load())
			}
		})
	}
}

func TestExternalDeactivatedMirroredUserDenies(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u-off", "alpha", "s1", user.StatusDeactivated)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Email: "a@example.test", EmailVerified: true})
	if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrExternalSubjectDenied) {
		t.Fatalf("Verify = %v, want denied", err)
	}
	denied := h.eventsOf(securityevent.TypeExternalSubjectDenied)
	if len(denied) != 1 || denied[0].Details["reason"] != externalReasonInactive || denied[0].UserID != "u-off" {
		t.Fatalf("denied events = %+v", denied)
	}
	if h.store.provisionCalls.Load() != 0 {
		t.Fatal("a mirrored subject was provisioned again")
	}
}

func TestExternalStoreReadErrorIsUnavailable(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.store.getErr = errors.New("connection refused")
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("Verify = %v, want unavailable only", err)
	}
}

func TestExternalJITOffRecordsUnknown(t *testing.T) {
	h := newExtHarness(t, nil)
	raw := h.token(h.providerB, sdkauth.Identity{Subject: "s1", Email: "a@example.test", EmailVerified: true})
	if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrExternalSubjectUnknown) {
		t.Fatalf("Verify = %v, want unknown", err)
	}
	if len(h.eventsOf(securityevent.TypeExternalSubjectUnknown)) != 1 || h.store.provisionCalls.Load() != 0 {
		t.Fatal("JIT-off subject was not denied with one external_subject_unknown event")
	}
}

func TestExternalJITProvisionsOnceWithMirror(t *testing.T) {
	cases := []struct {
		name           string
		identity       sdkauth.Identity
		kind           identifier.Kind
		verified       bool
		wantInvitation bool
	}{
		{"verified email", sdkauth.Identity{Subject: "s1", Email: "New@Example.test", EmailVerified: true, DisplayName: "Ada"}, identifier.KindEmail, true, true},
		{"unverified email", sdkauth.Identity{Subject: "s2", Email: "u@example.test"}, identifier.KindEmail, false, false},
		{"phone only", sdkauth.Identity{Subject: "s3", Phone: "+15555550100", PhoneVerified: true}, identifier.KindPhone, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newExtHarness(t, nil)
			raw := h.token(h.providerA, tc.identity)
			principal, cred, err := h.engine.Verify(context.Background(), raw, TransportCookie)
			if err != nil {
				t.Fatal(err)
			}
			if principal.Type != PrincipalUser || principal.ID == "" || principal.ID == tc.identity.Subject {
				t.Fatalf("principal = %+v, want an internal user id", principal)
			}
			if cred.Kind != CredentialExternalToken || cred.Transport != TransportCookie || cred.Identity.Subject != tc.identity.Subject || cred.Issuer != "" {
				t.Fatalf("credential = %+v", cred)
			}
			if h.store.provisionCalls.Load() != 1 || h.store.provisioned[0].External == nil || h.store.provisioned[0].External.Provider != "alpha" {
				t.Fatalf("Provision calls=%d creds=%+v, want one with External set", h.store.provisionCalls.Load(), h.store.provisioned)
			}
			ident := h.store.idents[0]
			if ident.Kind != tc.kind || ident.LoginEnabled || ident.RecoveryEnabled || !ident.NotificationEnabled || ident.Verified() != tc.verified {
				t.Fatalf("identifier = %+v, want notification-only %s verified=%v", ident, tc.kind, tc.verified)
			}
			if got := len(h.invitations.calls); (got == 1) != tc.wantInvitation {
				t.Fatalf("pending invitation resolutions = %v", h.invitations.calls)
			}
			if tc.wantInvitation && h.invitations.calls[0] != "new@example.test|"+principal.ID {
				t.Fatalf("invitation resolved for %q", h.invitations.calls[0])
			}
			if provisioned := h.eventsOf(securityevent.TypeExternalSubjectProvisioned); len(provisioned) != 1 || provisioned[0].UserID != principal.ID {
				t.Fatalf("provisioned events = %+v", provisioned)
			}

			again, _, err := h.engine.Verify(context.Background(), raw, TransportHeader)
			if err != nil || again != principal || h.store.provisionCalls.Load() != 1 {
				t.Fatalf("second Verify = %+v, %v (provisions %d), want the mirror hit", again, err, h.store.provisionCalls.Load())
			}
		})
	}
}

func TestExternalStoreDroppingExternalFailsClosed(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.dropExternal = true
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Email: "a@example.test", EmailVerified: true})
	for i := range 3 {
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("attempt %d = %v, want fail-closed unavailable", i, err)
		}
	}
	if got := h.store.provisionCalls.Load(); got != 1 {
		t.Fatalf("Provision calls = %d, want 1: a broken store must not create a user per request", got)
	}
	if len(h.invitations.calls) != 0 || len(h.eventsOf(securityevent.TypeExternalSubjectProvisioned)) != 0 {
		t.Fatal("a fail-closed provisioning resolved invitations or recorded success")
	}
}

func TestExternalCollisionDeniesAndRecords(t *testing.T) {
	for _, verified := range []bool{true, false} {
		h := newExtHarness(t, nil)
		h.store.addUser("native", "", "", user.StatusActive)
		h.store.idents = append(h.store.idents, identifier.Identifier{UserID: "native", Kind: identifier.KindEmail, NormalizedValue: "taken@example.test", LoginEnabled: true, RecoveryEnabled: true, VerifiedAt: time.Now()})
		raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Email: "Taken@example.test", EmailVerified: verified})
		if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, ErrExternalSubjectCollision) {
			t.Fatalf("verified=%v: Verify = %v, want collision", verified, err)
		}
		collisions := h.eventsOf(securityevent.TypeExternalSubjectCollision)
		if len(collisions) != 1 || collisions[0].UserID != "native" || collisions[0].Details["provider"] != "alpha" {
			t.Fatalf("collision events = %+v", collisions)
		}
		if h.store.provisionCalls.Load() != 0 {
			t.Fatal("a colliding subject was provisioned")
		}
	}
}

func TestExternalPolicyDenialsCarryReason(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*ExternalConfig)
		identity  sdkauth.Identity
		provErr   error
		reason    string
	}{
		{"tenant", func(c *ExternalConfig) { c.Providers[0].AllowedTenants = []string{"org-1"} }, sdkauth.Identity{Subject: "s1", Tenant: "org-2", Email: "a@example.test"}, nil, externalReasonTenant},
		{"missing tenant", func(c *ExternalConfig) { c.Providers[0].AllowedTenants = []string{"org-1"} }, sdkauth.Identity{Subject: "s1", Email: "a@example.test"}, nil, externalReasonTenant},
		{"unverified email", func(c *ExternalConfig) { c.Providers[0].RequireVerifiedEmail = true }, sdkauth.Identity{Subject: "s1", Email: "a@example.test"}, nil, externalReasonUnverifiedEmail},
		{"sign-in method", nil, sdkauth.Identity{Subject: "s1"}, sdkauth.ErrSignInMethodDenied, externalReasonSignInMethod},
		{"no identifier", nil, sdkauth.Identity{Subject: "s1"}, nil, externalReasonNoIdentifier},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newExtHarness(t, tc.configure)
			raw := h.token(h.providerA, tc.identity)
			h.providerA.err = tc.provErr
			if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("Verify = %v, want a denial", err)
			}
			denied := h.eventsOf(securityevent.TypeExternalSubjectDenied)
			if len(denied) != 1 || denied[0].Details["reason"] != tc.reason || denied[0].Details["provider"] != "alpha" {
				t.Fatalf("denied events = %+v, want reason %q", denied, tc.reason)
			}
			if h.store.provisionCalls.Load() != 0 {
				t.Fatal("a denied subject was provisioned")
			}
		})
	}
	h := newExtHarness(t, func(c *ExternalConfig) { c.Providers[0].AllowedTenants = []string{"org-1"} })
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Tenant: "org-1", Email: "a@example.test"})
	if _, _, err := h.engine.Verify(context.Background(), raw, TransportHeader); err != nil {
		t.Fatalf("allowed tenant denied: %v", err)
	}
}

// verifyLive authenticates raw through the standalone credential service.
func (h *extHarness) authenticate(t *testing.T, raw string) context.Context {
	t.Helper()
	ctx, err := h.engine.Authenticate(context.Background(), CredentialExternalToken, TransportHeader, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestExternalCachedRevocationIsFactsNotVerdict(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	revokedAt := h.clock.Now()
	h.revocation.set("s1", sdkauth.RevocationFacts{ValidAfter: revokedAt}, nil)

	old := h.token(h.providerA, sdkauth.Identity{Subject: "s1", AuthenticatedAt: revokedAt.Add(-time.Hour)})
	if _, err := h.engine.RequireLive(h.authenticate(t, old), LivenessOnMutation, true); !errors.Is(err, ErrRevoked) {
		t.Fatalf("token from before revocation = %v, want revoked", err)
	}
	if denied := h.eventsOf(securityevent.TypeExternalSubjectDenied); len(denied) != 1 || denied[0].Details["reason"] != externalReasonRevoked {
		t.Fatalf("denied events = %+v", denied)
	}
	if _, err := h.engine.RequireLive(h.authenticate(t, old), LivenessOnMutation, false); err != nil {
		t.Fatalf("a safe read under the default tier re-checked: %v", err)
	}

	h.providerA.identities = map[string]sdkauth.Identity{}
	fresh := h.token(h.providerA, sdkauth.Identity{Subject: "s1", AuthenticatedAt: revokedAt.Add(time.Second)})
	if _, err := h.engine.RequireLive(h.authenticate(t, fresh), LivenessLive, false); err != nil {
		t.Fatalf("re-authenticated token denied by the cached fact: %v", err)
	}
	if got := h.revocation.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want the second check served from cached facts", got)
	}

	h.revocation.set("s1", sdkauth.RevocationFacts{Disabled: true}, nil)
	h.clock.advance(defaultRevocationCacheTTL + time.Second)
	if _, err := h.engine.RequireLive(h.authenticate(t, fresh), LivenessLive, false); !errors.Is(err, ErrRevoked) {
		t.Fatalf("disabled subject = %v, want revoked", err)
	}
}

func TestExternalOutageServesStaleWithinGraceThenUnavailable(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true); err != nil {
		t.Fatal(err)
	}

	h.revocation.set("s1", sdkauth.RevocationFacts{}, &sdkauth.UnavailableError{Kind: sdkauth.UnavailableUpstream})
	h.clock.advance(defaultRevocationCacheTTL + time.Second)
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true); err != nil {
		t.Fatalf("within grace = %v, want stale facts served", err)
	}
	if stats := h.engine.RevocationStats(); stats.StaleServed != 1 || stats.ProviderErrors != 1 {
		t.Fatalf("stats = %+v", stats)
	}

	h.clock.advance(defaultRevocationOutageGrace)
	_, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true)
	if !errors.Is(err, sdkauth.ErrProviderUnavailable) || !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("past grace = %v, want ErrProviderUnavailable", err)
	}
	if stats := h.engine.RevocationStats(); stats.FailClosed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if got := len(h.eventsOf(securityevent.TypeExternalRevocationUnavailable)); got != 1 {
		t.Fatalf("outage events = %d, want 1", got)
	}
}

func TestExternalFailOpenRecordsOneEventPerEpisode(t *testing.T) {
	h := newExtHarness(t, func(c *ExternalConfig) { c.Providers[0].RevocationFailOpen = true })
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, &sdkauth.UnavailableError{Kind: sdkauth.UnavailableNetwork})
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	for i := range 1000 {
		ctx, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessOnMutation, true)
		if err != nil {
			t.Fatalf("request %d = %v, want fail-open pass", i, err)
		}
		if proof, _ := proofFor(ctx, h.engine); !proof.externalLiveAt.IsZero() {
			t.Fatal("a fail-open pass was stamped as a full re-check")
		}
	}
	if got := len(h.eventsOf(securityevent.TypeExternalRevocationUnavailable)); got != 1 {
		t.Fatalf("outage events = %d, want exactly one per episode", got)
	}
	if stats := h.engine.RevocationStats(); stats.FailOpen != 1000 {
		t.Fatalf("stats = %+v", stats)
	}
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("Live() during the outage = %v, want fail-closed", err)
	}

	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true); err != nil {
		t.Fatalf("after recovery = %v", err)
	}
	h.revocation.set("s1", sdkauth.RevocationFacts{}, &sdkauth.UnavailableError{Kind: sdkauth.UnavailableNetwork})
	h.clock.advance(defaultRevocationOutageGrace + time.Second)
	if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessOnMutation, true); err != nil {
		t.Fatal(err)
	}
	if got := len(h.eventsOf(securityevent.TypeExternalRevocationUnavailable)); got != 2 {
		t.Fatalf("outage events after a second episode = %d, want 2", got)
	}
}

func TestExternalCacherErrorIsAMiss(t *testing.T) {
	h := newExtHarness(t, func(c *ExternalConfig) { c.RevocationCache = failingCache{cacher.NewMemory()} })
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	for range 2 {
		if _, err := h.engine.RequireLive(h.authenticate(t, raw), LivenessLive, true); err != nil {
			t.Fatalf("cache outage became an authentication failure: %v", err)
		}
	}
	stats := h.engine.RevocationStats()
	if stats.CacherErrors == 0 || stats.Misses != 2 || stats.FailClosed != 0 || h.revocation.calls.Load() != 2 {
		t.Fatalf("stats = %+v calls=%d, want misses served by the provider", stats, h.revocation.calls.Load())
	}
}

func TestExternalRevocationSingleFlight(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	h.revocation.gate = make(chan struct{})
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		ctx := h.authenticate(t, raw)
		wg.Go(func() {
			_, err := h.engine.RequireLive(ctx, LivenessLive, true)
			errs <- err
		})
	}
	for h.revocation.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(h.revocation.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := h.revocation.calls.Load(); got > 2 {
		t.Fatalf("provider calls = %d for 20 concurrent checks", got)
	}
}

func TestExternalNestedGateReadsOnce(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.revocation.set("s1", sdkauth.RevocationFacts{}, nil)
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1"})
	ctx := h.authenticate(t, raw)
	ctx, err := h.engine.RequireLive(ctx, LivenessOnMutation, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.RequireLive(ctx, LivenessLive, true); err != nil {
		t.Fatal(err)
	}
	stats := h.engine.RevocationStats()
	if users, checks := h.store.userGets.Load(), h.revocation.calls.Load(); users != 1 || checks != 1 || stats.Hits+stats.Misses != 1 {
		t.Fatalf("users.Get=%d CheckRevocation=%d revocation lookups=%d, want exactly one of each", users, checks, stats.Hits+stats.Misses)
	}
}

func TestExternalCheckLiveReadsLocalStatus(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "beta", "s1", user.StatusActive)
	raw := h.token(h.providerB, sdkauth.Identity{Subject: "s1"})
	principal, cred, err := h.engine.Verify(context.Background(), raw, TransportHeader)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.engine.CheckLive(context.Background(), principal, cred, LivenessLive); err != nil {
		t.Fatal(err)
	}
	h.store.addUser("u1", "beta", "s1", user.StatusDeactivated)
	if err := h.engine.CheckLive(context.Background(), principal, cred, LivenessLive); !errors.Is(err, ErrExternalSubjectDenied) {
		t.Fatalf("deactivated user = %v, want the local kill switch", err)
	}
	if err := h.engine.CheckLive(context.Background(), principal, cred, LivenessStateless); err != nil {
		t.Fatalf("Stateless re-checked: %v", err)
	}
}

func TestExternalStandaloneCredentialService(t *testing.T) {
	h := newExtHarness(t, nil)
	h.store.addUser("u1", "alpha", "s1", user.StatusActive)
	h.store.idents = append(h.store.idents, identifier.Identifier{UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "a@example.test", NotificationEnabled: true, VerifiedAt: time.Now()})
	raw := h.token(h.providerA, sdkauth.Identity{Subject: "s1", Methods: []string{"pwd", "mfa"}})
	if _, err := h.engine.Authenticate(context.Background(), CredentialAccessToken, TransportHeader, raw); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("access-token kind = %v, want rejection", err)
	}
	ctx := h.authenticate(t, raw)
	cred, ok := h.engine.CurrentCredential(ctx)
	if !ok || cred.Kind != CredentialExternalToken {
		t.Fatalf("CurrentCredential = %+v, %v", cred, ok)
	}
	cred.Identity.Methods[0] = "changed"
	if again, _ := h.engine.CurrentCredential(ctx); again.Identity.Methods[0] != "pwd" {
		t.Fatal("a caller mutated the stashed identity")
	}
	if id, ok := h.engine.CurrentUser(ctx); !ok || id != "u1" {
		t.Fatalf("CurrentUser = %q, %v", id, ok)
	}
	if _, ok := h.engine.CurrentSessionID(ctx); ok || h.engine.MachineEnabled() || h.engine.SessionLifetime() != 0 || !h.engine.ExternalEnabled() {
		t.Fatal("external-only service advertised a native rail")
	}
	info, err := h.engine.Resolve(ctx, Principal{Type: PrincipalUser, ID: "u1"})
	if err != nil || len(info.Addresses) != 1 || info.Addresses[0].Value != "a@example.test" {
		t.Fatalf("Resolve = %+v, %v", info, err)
	}
	if _, err := newExtHarness(t, nil).engine.RequireLive(ctx, LivenessLive, true); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("another engine accepted this engine's proof: %v", err)
	}
}

func TestNewExternalAuthenticatorValidation(t *testing.T) {
	repos := func() ExternalRepositories {
		s := newExtStore()
		return ExternalRepositories{Users: extUsers{s}, Identifiers: extIdentifiers{s}, ExternalSubjects: extSubjects{s}}
	}
	provider := func(issuer string) *extProvider { return &extProvider{issuer: issuer} }
	var typedNil *extRevocation
	cases := map[string]struct {
		repos ExternalRepositories
		cfg   ExternalConfig
		mode  environment.Mode
	}{
		"no providers":                          {repos(), ExternalConfig{}, environment.ModeDevelopment},
		"no mirror":                             {ExternalRepositories{Users: repos().Users, Identifiers: repos().Identifiers}, ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA)}}}, environment.ModeDevelopment},
		"blank name":                            {repos(), ExternalConfig{Providers: []ExternalProvider{{Provider: provider(extIssuerA)}}}, environment.ModeDevelopment},
		"relative issuer":                       {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider("issuer")}}}, environment.ModeDevelopment},
		"duplicate issuer":                      {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA)}, {Name: "a", Provider: provider(extIssuerA)}}}, environment.ModeDevelopment},
		"typed-nil revocation":                  {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA), Revocation: typedNil}}}, environment.ModeDevelopment},
		"grace below ttl":                       {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA)}}, RevocationCacheTTL: time.Minute, RevocationOutageGrace: time.Second}, environment.ModeDevelopment},
		"timeout too long":                      {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA)}}, RevocationTimeout: 10 * time.Second}, environment.ModeDevelopment},
		"bad cookie name":                       {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA), CookieName: "bad name"}}}, environment.ModeDevelopment},
		"production http issuer":                {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider("http://issuer.example.test")}}}, environment.ModeProduction},
		"production provisioning without keyer": {repos(), ExternalConfig{Providers: []ExternalProvider{{Name: "a", Provider: provider(extIssuerA), ProvisionOnFirstVerification: true}}}, environment.ModeProduction},
	}
	for name, tc := range cases {
		if _, err := NewExternalAuthenticator(tc.repos, tc.cfg, tc.mode); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
	shared := ExternalConfig{Providers: []ExternalProvider{{Name: "firm", Provider: provider(extIssuerA)}, {Name: "firm", Provider: provider(extIssuerB), CookieName: "__session"}}}
	if _, err := NewExternalAuthenticator(repos(), shared, environment.ModeDevelopment); err != nil {
		t.Fatalf("two issuers sharing one mirror name rejected: %v", err)
	}
}
