package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// Denial reasons recorded on TypeExternalSubjectDenied.
const (
	externalReasonTenant            = "tenant"
	externalReasonSignInMethod      = "sign_in_method"
	externalReasonUnverifiedEmail   = "unverified_email"
	externalReasonNoIdentifier      = "no_identifier"
	externalReasonInvalidIdentifier = "invalid_identifier"
	externalReasonInactive          = "inactive"
	externalReasonRevoked           = "revoked"
	externalReasonAgeExceeded       = "authentication_age_exceeded"
	externalReasonAuthTimeMissing   = "authenticated_at_missing"
)

// authenticationAgeLeeway absorbs clock skew between the provider and this
// host when MaxAuthenticationAge is applied.
const authenticationAgeLeeway = 60 * time.Second

const latencySamples = 256

var (
	// ErrExternalSubjectUnknown denies a verified subject with no local mapping on
	// a provider that does not provision. It matches sdk.ErrUnauthorized.
	ErrExternalSubjectUnknown = fmt.Errorf("authentication: external subject is unknown: %w", sdk.ErrUnauthorized)
	// ErrExternalSubjectCollision denies provisioning whose address is an existing
	// account's login or recovery identifier. It matches sdk.ErrUnauthorized.
	ErrExternalSubjectCollision = fmt.Errorf("authentication: external subject collides with an existing account: %w", sdk.ErrUnauthorized)
	// ErrExternalSubjectDenied denies a verified subject by policy. It matches
	// sdk.ErrUnauthorized.
	ErrExternalSubjectDenied = fmt.Errorf("authentication: external subject denied: %w", sdk.ErrUnauthorized)
	// ErrRevoked denies a credential the provider reports revoked or disabled. It
	// matches sdk.ErrUnauthorized.
	ErrRevoked = fmt.Errorf("authentication: credential revoked: %w", sdk.ErrUnauthorized)
	// ErrIdentityStoreUnavailable reports a local identity read or write that
	// could not complete. It matches sdk.ErrUnavailable.
	ErrIdentityStoreUnavailable = fmt.Errorf("authentication: identity store unavailable: %w", sdk.ErrUnavailable)

	errCredentialRejected = fmt.Errorf("authentication: credential rejected: %w", sdk.ErrUnauthorized)
	errNotLive            = fmt.Errorf("authentication: credential is not live: %w", sdk.ErrUnauthorized)
	errMirrorNotPersisted = fmt.Errorf("authentication: the user store did not persist the external subject: %w", sdk.ErrUnavailable)
)

var _ sdk.IdentityResolver = (*ExternalAuthenticator)(nil)

// ExternalAuthenticator verifies tokens from wired external identity providers
// and maps them to local users through the subject mirror. Hybrid hosts reach it
// through Service (WithExternal); external-only hosts construct it directly and
// use it as their credential service, with no signer, sessions or mail.
type ExternalAuthenticator struct {
	providers   map[string]externalProvider
	users       user.UserRepository
	identifiers identifier.IdentifierRepository
	subjects    externalsubject.Repository
	events      *securityevent.Recorder
	invitations invitationResolver
	normalizer  identifier.Normalizer
	keyer       identifierKeyer
	ids         sdk.IDGenerator
	now         func() time.Time
	logger      *slog.Logger
	// registration is the host's IdentityConfig.Registration (open for an
	// external-only engine).
	registration RegistrationMode

	cache   *cacher.Cache
	logins  *cacher.Cache
	ttl     time.Duration
	grace   time.Duration
	timeout time.Duration

	// mirrorBroken latches when a store drops InitialCredentials.External, so a
	// broken store cannot create one orphan user per request.
	mirrorBroken atomic.Bool

	flightMu sync.Mutex
	flights  map[string]*revocationFlight

	episodeMu sync.Mutex
	episodes  map[string]*revocationEpisode

	stats revocationCounters
}

// RevocationStats is a snapshot of the revocation re-check counters since
// construction. Latencies cover the most recent provider calls.
type RevocationStats struct {
	Hits           uint64
	Misses         uint64
	StaleServed    uint64
	FailOpen       uint64
	FailClosed     uint64
	ProviderErrors uint64
	CacherErrors   uint64
	LatencyP50     time.Duration
	LatencyMax     time.Duration
}

type externalProvider struct {
	ExternalProvider
	issuer string
}

type revocationFlight struct {
	done  chan struct{}
	facts sdkauth.RevocationFacts
	err   error
}

type revocationEpisode struct {
	start          time.Time
	affected       int
	staleWarned    bool
	failOpenWarned bool
}

type revocationCounters struct {
	hits, misses, staleServed, failOpen, failClosed, providerErrors, cacherErrors atomic.Uint64

	mu        sync.Mutex
	latencies []time.Duration
	next      int
}

type cachedRevocationFacts struct {
	Disabled   bool      `json:"disabled"`
	ValidAfter time.Time `json:"valid_after"`
	CheckedAt  time.Time `json:"checked_at"`
}

// NewExternalAuthenticator builds the engine for an external-only host. Hybrid
// hosts use WithExternal on New instead.
func NewExternalAuthenticator(repos ExternalRepositories, cfg ExternalConfig, runtimeMode environment.Mode, opts ...ExternalOption) (*ExternalAuthenticator, error) {
	var o externalOptions
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication NewExternalAuthenticator: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&o)
	}
	if err := environment.ValidateMode(runtimeMode); err != nil {
		return nil, err
	}
	if o.normalizer != nil && nilDependency(o.normalizer) || o.keyer != nil && nilDependency(o.keyer) {
		return nil, fmt.Errorf("authentication: external option contains a typed nil: %w", sdk.ErrInvalidInput)
	}
	log := o.logger
	if log == nil {
		log = slog.Default()
	}
	cfg, err := validateExternal(cfg, repos, runtimeMode, o.keyer, "", log)
	if err != nil {
		return nil, err
	}
	if err := checkExternalPosture(cfg, runtimeMode, log); err != nil {
		return nil, err
	}
	return newExternalAuthenticator(repos, cfg, runtimeMode, o), nil
}

// newExternalAuthenticator assembles an already validated configuration.
func newExternalAuthenticator(repos ExternalRepositories, cfg ExternalConfig, mode environment.Mode, o externalOptions) *ExternalAuthenticator {
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	if o.normalizer == nil {
		o.normalizer = identifier.DefaultNormalizer{}
	}
	if o.recorder == nil {
		o.recorder = newSecurityEventRecorder(repos.SecurityEvents, o.ids, o.clock, o.logger, nil)
	}
	e := &ExternalAuthenticator{
		providers:   make(map[string]externalProvider, len(cfg.Providers)),
		users:       repos.Users,
		identifiers: repos.Identifiers,
		subjects:    repos.ExternalSubjects,
		events:      o.recorder,
		invitations: repos.Invitations,
		normalizer:  o.normalizer,
		keyer:       o.keyer,
		ids:         o.ids,
		now:         o.clock,
		logger:      o.logger,
		ttl:         cfg.RevocationCacheTTL,
		grace:       cfg.RevocationOutageGrace,
		timeout:     cfg.RevocationTimeout,
		flights:     map[string]*revocationFlight{},
		episodes:    map[string]*revocationEpisode{},
	}
	e.registration = o.registration
	store := cfg.RevocationCache
	if store == nil {
		store = cacher.NewMemory(cacher.WithMaxEntries(cfg.RevocationCacheEntries))
	}
	e.cache = cacher.New(store, cacher.WithNamespace(revocationCacheNamespace), cacher.WithOnError(func(context.Context, string, error) {
		e.stats.cacherErrors.Add(1)
	}))
	logins := cfg.LoginRecordCache
	if logins == nil {
		logins = cacher.NewMemory(cacher.WithMaxEntries(cfg.LoginRecordCacheEntries))
	}
	e.logins = cacher.New(logins, cacher.WithNamespace(loginRecordCacheNamespace))
	for _, p := range cfg.Providers {
		issuer := p.Provider.Issuer()
		e.providers[issuer] = externalProvider{ExternalProvider: p, issuer: issuer}
		if mode == environment.ModeProduction && p.RevocationFailOpen {
			e.logger.Warn("auth: production posture requirement waived", "posture_waiver", "RevocationFailOpen", "runtime_mode", mode.String(), "provider", p.Name)
		}
	}
	return e
}

// ClassifyBearer reports whether raw names a wired provider's issuer. It reads
// the unverified iss only to select a verifier; it proves nothing.
func (e *ExternalAuthenticator) ClassifyBearer(raw string) (CredentialKind, bool) {
	iss, present, ok := peekIssuer(raw)
	if !ok || !present || !e.wired(iss) {
		return "", false
	}
	return CredentialExternalToken, true
}

func (e *ExternalAuthenticator) wired(issuer string) bool {
	_, ok := e.providers[issuer]
	return ok
}

// Verify authenticates raw with the ONE provider its unverified iss names, then
// maps the subject to an active local user, provisioning it when the provider
// allows. The credential's Assurance is AAL2 when the provider asserted
// multi-factor authentication, AAL1 otherwise; a provider with RequireMFA
// denies an AAL1 token with ErrMFARequired before the subject resolves, so the
// denial provisions nothing and its row carries no UserID. A provider failure
// never falls through to another provider. Errors match sdk.ErrUnauthorized
// (deny) or sdk.ErrUnavailable (cannot decide).
func (e *ExternalAuthenticator) Verify(ctx context.Context, raw string, transport Transport) (Principal, Credential, error) {
	if transport != TransportHeader && transport != TransportCookie {
		return Principal{}, Credential{}, errCredentialRejected
	}
	iss, present, ok := peekIssuer(raw)
	if !ok || !present {
		return Principal{}, Credential{}, sdkauth.ErrInvalidCredential
	}
	p, ok := e.providers[iss]
	if !ok {
		return Principal{}, Credential{}, sdkauth.ErrInvalidCredential
	}
	identity, err := p.Provider.Authenticate(ctx, sdkauth.Credential{Transport: sdkauth.Transport(transport), Raw: raw})
	if err != nil {
		switch {
		case errors.Is(err, sdk.ErrUnavailable):
			return Principal{}, Credential{}, err
		case errors.Is(err, sdkauth.ErrSignInMethodDenied):
			e.deny(ctx, p, "", externalReasonSignInMethod)
		}
		return Principal{}, Credential{}, sdkauth.ErrInvalidCredential
	}
	if identity.Issuer != iss || identity.Subject == "" {
		return Principal{}, Credential{}, sdkauth.ErrIssuerMismatch
	}
	if len(p.AllowedTenants) > 0 && !slices.Contains(p.AllowedTenants, identity.Tenant) {
		return Principal{}, Credential{}, e.deny(ctx, p, "", externalReasonTenant)
	}
	if p.RequireVerifiedEmail && identity.Email != "" && !identity.EmailVerified {
		return Principal{}, Credential{}, e.deny(ctx, p, "", externalReasonUnverifiedEmail)
	}
	assurance := externalAssurance(identity)
	if p.RequireMFA && !assurance.AtLeast(session.AssuranceAAL2) {
		e.events.Record(ctx, mfaDenialEvent(MFADenial{
			Gate:       MFAGateProvider,
			Credential: Credential{Kind: CredentialExternalToken, Transport: transport, Assurance: assurance, Methods: []session.MethodKind{session.MethodExternal}},
		}, p.Name))
		return Principal{}, Credential{}, ErrMFARequired
	}
	if reason := e.authenticationAgeRefusal(p, identity); reason != "" {
		return Principal{}, Credential{}, e.deny(ctx, p, "", reason)
	}
	userID, err := e.resolveSubject(ctx, p, identity)
	if err != nil {
		return Principal{}, Credential{}, err
	}
	e.recordLogin(ctx, p, userID, raw, identity.ExpiresAt, assurance)
	return Principal{Type: PrincipalUser, ID: userID}, Credential{Kind: CredentialExternalToken, Transport: transport, Identity: identity.Clone(), Assurance: assurance, Methods: []session.MethodKind{session.MethodExternal}}, nil
}

// authenticationAgeRefusal applies p.MaxAuthenticationAge to identity on
// every request, before the subject resolves, so a denied token never
// provisions: an identity with no authentication time, or one older than the
// bound plus authenticationAgeLeeway, is refused with its reason. A zero bound
// refuses nothing.
func (e *ExternalAuthenticator) authenticationAgeRefusal(p externalProvider, identity sdkauth.Identity) string {
	switch {
	case p.MaxAuthenticationAge <= 0:
		return ""
	case identity.AuthenticatedAt.IsZero():
		return externalReasonAuthTimeMissing
	case e.now().Sub(identity.AuthenticatedAt) > p.MaxAuthenticationAge+authenticationAgeLeeway:
		return externalReasonAgeExceeded
	}
	return ""
}

// recordLogin records external_login once per token per login-record cache,
// with the assurance the provider asserted (aal1 or aal2), keyed by the
// token's SHA-256 so the cache never holds the token. A cache hit
// skips the row; a miss, an evicted entry or a cache error records it, so a
// cache fault costs a duplicate row, never a missing one. The entry lives until
// the token expires, at most 24 hours and at least one minute.
func (e *ExternalAuthenticator) recordLogin(ctx context.Context, p externalProvider, userID, raw string, expiresAt time.Time, assurance session.AssuranceLevel) {
	sum := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(sum[:])
	if _, found, err := e.logins.Get(ctx, key); err == nil && found {
		return
	}
	e.events.Record(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeExternalLogin,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"provider": p.Name, "assurance": string(assurance)},
	})
	ttl := max(min(expiresAt.Sub(e.now()), maxLoginRecordTTL), minLoginRecordTTL)
	_ = e.logins.Set(ctx, key, []byte{1}, ttl)
}

// CheckLive re-checks an external credential the caller already verified: the
// local user status, then the provider's revocation facts compared to the
// token's authentication time. The caller selects the tier; LivenessOnMutation
// means "this request mutates under the default tier". LivenessStateless passes.
func (e *ExternalAuthenticator) CheckLive(ctx context.Context, principal Principal, cred Credential, tier LivenessTier) error {
	if tier == LivenessStateless {
		return nil
	}
	_, err := e.recheck(ctx, principal, cred, tier, false)
	return err
}

// RevocationStats returns the revocation counters.
func (e *ExternalAuthenticator) RevocationStats() RevocationStats {
	stats := RevocationStats{
		Hits:           e.stats.hits.Load(),
		Misses:         e.stats.misses.Load(),
		StaleServed:    e.stats.staleServed.Load(),
		FailOpen:       e.stats.failOpen.Load(),
		FailClosed:     e.stats.failClosed.Load(),
		ProviderErrors: e.stats.providerErrors.Load(),
		CacherErrors:   e.stats.cacherErrors.Load(),
	}
	e.stats.mu.Lock()
	samples := slices.Clone(e.stats.latencies)
	e.stats.mu.Unlock()
	if len(samples) > 0 {
		slices.Sort(samples)
		stats.LatencyP50 = samples[len(samples)/2]
		stats.LatencyMax = samples[len(samples)-1]
	}
	return stats
}

// RunHeartbeat logs an INFO line with the counter deltas every interval until
// ctx is done. A missing beat is the alarm. It returns at once when interval is
// not positive.
func (e *ExternalAuthenticator) RunHeartbeat(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := e.RevocationStats()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := e.RevocationStats()
			e.logger.Info("external revocation alive",
				"hits", cur.Hits-last.Hits,
				"misses", cur.Misses-last.Misses,
				"stale_served", cur.StaleServed-last.StaleServed,
				"fail_open", cur.FailOpen-last.FailOpen,
				"fail_closed", cur.FailClosed-last.FailClosed,
				"provider_errors", cur.ProviderErrors-last.ProviderErrors,
				"cacher_errors", cur.CacherErrors-last.CacherErrors,
				"latency_p50", cur.LatencyP50,
				"latency_max", cur.LatencyMax,
			)
			last = cur
		}
	}
}

// Authenticate verifies an external token and records its principal and
// credential in the returned context. It is the external-only host's credential
// service; any other kind is rejected.
func (e *ExternalAuthenticator) Authenticate(ctx context.Context, kind CredentialKind, transport Transport, raw string) (context.Context, error) {
	ctx = clearCredential(ctx)
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	if kind != CredentialExternalToken {
		return ctx, errCredentialRejected
	}
	principal, cred, err := e.Verify(ctx, raw, transport)
	if err != nil {
		return ctx, err
	}
	return withProof(ctx, e, principal, cred), nil
}

// RequireLive applies tier to the credential this engine authenticated on ctx.
// unsafe reports a mutating request. A passed re-check is stamped on the proof,
// so a nested gate on the same request does not repeat it.
func (e *ExternalAuthenticator) RequireLive(ctx context.Context, tier LivenessTier, unsafe bool) (context.Context, error) {
	proof, ok := proofFor(ctx, e)
	if !ok || ctx.Err() != nil {
		return clearCredential(ctx), errNotLive
	}
	return e.requireLiveProof(ctx, proof, tier, unsafe)
}

// ExternalEnabled is true: every wired provider's tokens are admitted.
func (e *ExternalAuthenticator) ExternalEnabled() bool { return true }

// MachineEnabled is false: an external-only host has no API keys.
func (e *ExternalAuthenticator) MachineEnabled() bool { return false }

// SessionLifetime is zero: an external-only host mints no sessions.
func (e *ExternalAuthenticator) SessionLifetime() time.Duration { return 0 }

// CurrentCredential returns the credential this engine verified on ctx.
func (e *ExternalAuthenticator) CurrentCredential(ctx context.Context) (Credential, bool) {
	proof, ok := proofFor(ctx, e)
	if !ok {
		return Credential{}, false
	}
	return proof.credential.clone(), true
}

// CurrentPrincipal returns the Principal stashed on ctx, if any.
func (e *ExternalAuthenticator) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return sdk.PrincipalFromContext(ctx)
}

// CurrentUser returns the authenticated user id stashed on ctx, if any.
func (e *ExternalAuthenticator) CurrentUser(ctx context.Context) (string, bool) {
	p, ok := sdk.PrincipalFromContext(ctx)
	if !ok || p.Type != sdk.PrincipalTypeUser {
		return "", false
	}
	return p.ID, true
}

// CurrentSessionID is always false: external credentials have no native session.
func (e *ExternalAuthenticator) CurrentSessionID(context.Context) (string, bool) { return "", false }

// Resolve projects a user principal's stored display name and verified
// addresses. Any other principal type is not found.
func (e *ExternalAuthenticator) Resolve(ctx context.Context, p sdk.Principal) (sdk.IdentityInfo, error) {
	if p.Type != sdk.PrincipalTypeUser {
		return sdk.IdentityInfo{}, resolveNotFound(p)
	}
	u, err := e.users.Get(ctx, p.ID)
	if err != nil {
		return sdk.IdentityInfo{}, err
	}
	addresses, err := projectAddresses(ctx, e.identifiers, p.ID)
	if err != nil {
		return sdk.IdentityInfo{}, err
	}
	return sdk.IdentityInfo{Principal: p, DisplayName: u.DisplayName, Addresses: addresses}, nil
}

// requireLiveProof is shared by the standalone engine and a hybrid Service.
// The proof exists only because Verify ran the local status read on this
// request, so the re-check reuses it rather than reading the user again.
func (e *ExternalAuthenticator) requireLiveProof(ctx context.Context, proof credentialProof, tier LivenessTier, unsafe bool) (context.Context, error) {
	if tier == LivenessStateless || (tier == LivenessOnMutation && !unsafe) || !proof.externalLiveAt.IsZero() {
		return ctx, nil
	}
	stamp, err := e.recheck(ctx, proof.principal, proof.credential, tier, true)
	if err != nil {
		return clearCredential(ctx), err
	}
	if stamp {
		ctx = withExternalLive(ctx, proof, e.now())
	}
	return ctx, nil
}

// recheck reports whether the credential passed with full assurance (stamp);
// a fail-open pass returns false so a stricter nested gate checks again.
func (e *ExternalAuthenticator) recheck(ctx context.Context, principal Principal, cred Credential, tier LivenessTier, localChecked bool) (bool, error) {
	if cred.Kind != CredentialExternalToken || principal.Type != PrincipalUser || principal.ID == "" {
		return false, errNotLive
	}
	p, ok := e.providers[cred.Identity.Issuer]
	if !ok {
		return false, errNotLive
	}
	if !localChecked {
		if err := e.requireActive(ctx, p, principal.ID); err != nil {
			return false, err
		}
	}
	if p.Revocation == nil {
		return true, nil
	}
	facts, failOpen, err := e.revocationFacts(ctx, p, cred.Identity.Subject, tier)
	if err != nil {
		return false, err
	}
	if failOpen {
		return false, nil
	}
	if facts.Disabled || (!facts.ValidAfter.IsZero() && cred.Identity.AuthenticatedAt.Before(facts.ValidAfter)) {
		e.deny(ctx, p, principal.ID, externalReasonRevoked)
		return false, ErrRevoked
	}
	return true, nil
}

// revocationFacts returns fresh facts from the cache or the provider. On a
// provider outage it serves stale facts within the grace window, then fails
// open (default tier, opted-in provider only) or closed.
func (e *ExternalAuthenticator) revocationFacts(ctx context.Context, p externalProvider, subject string, tier LivenessTier) (sdkauth.RevocationFacts, bool, error) {
	key := p.Name + ":" + subject
	cached, found := e.cachedFacts(ctx, key)
	if found && e.now().Before(cached.CheckedAt.Add(e.ttl)) {
		e.stats.hits.Add(1)
		return cached, false, nil
	}
	e.stats.misses.Add(1)
	facts, err := e.fetchFacts(ctx, p, key, subject)
	if err == nil {
		return facts, false, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return sdkauth.RevocationFacts{}, false, ctxErr
	}
	if found && e.now().Before(cached.CheckedAt.Add(e.grace)) {
		e.stats.staleServed.Add(1)
		e.outageServed(p, "stale")
		return cached, false, nil
	}
	if tier == LivenessOnMutation && p.RevocationFailOpen {
		e.stats.failOpen.Add(1)
		e.outageServed(p, "fail_open")
		return sdkauth.RevocationFacts{}, true, nil
	}
	e.stats.failClosed.Add(1)
	e.outageServed(p, "")
	return sdkauth.RevocationFacts{}, false, err
}

// cachedFacts treats every cache failure as a miss: a cache outage must never
// become an authentication outage.
func (e *ExternalAuthenticator) cachedFacts(ctx context.Context, key string) (sdkauth.RevocationFacts, bool) {
	raw, found, err := e.cache.Get(ctx, key)
	if err != nil || !found {
		return sdkauth.RevocationFacts{}, false
	}
	var value cachedRevocationFacts
	if err := json.Unmarshal(raw, &value); err != nil || value.CheckedAt.IsZero() {
		e.stats.cacherErrors.Add(1)
		return sdkauth.RevocationFacts{}, false
	}
	return sdkauth.RevocationFacts{Disabled: value.Disabled, ValidAfter: value.ValidAfter, CheckedAt: value.CheckedAt}, true
}

// fetchFacts calls the provider once per key at a time; concurrent callers
// share the leader's answer. The call outlives a canceled caller but not the
// configured timeout or the leader's deadline.
func (e *ExternalAuthenticator) fetchFacts(ctx context.Context, p externalProvider, key, subject string) (sdkauth.RevocationFacts, error) {
	e.flightMu.Lock()
	if f, ok := e.flights[key]; ok {
		e.flightMu.Unlock()
		select {
		case <-f.done:
			return f.facts, f.err
		case <-ctx.Done():
			return sdkauth.RevocationFacts{}, ctx.Err()
		}
	}
	f := &revocationFlight{done: make(chan struct{})}
	e.flights[key] = f
	e.flightMu.Unlock()
	defer func() {
		e.flightMu.Lock()
		delete(e.flights, key)
		e.flightMu.Unlock()
		close(f.done)
	}()

	timeout := e.timeout
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	started := time.Now()
	facts, err := p.Revocation.CheckRevocation(callCtx, p.issuer, subject)
	cancel()
	e.stats.observe(time.Since(started))

	now := e.now()
	switch {
	case err == nil:
	case errors.Is(err, sdk.ErrNotFound):
		facts = sdkauth.RevocationFacts{Disabled: true}
	default:
		e.stats.providerErrors.Add(1)
		if !errors.Is(err, sdkauth.ErrProviderUnavailable) {
			kind := sdkauth.UnavailableNetwork
			if errors.Is(err, context.DeadlineExceeded) {
				kind = sdkauth.UnavailableDeadline
			}
			err = &sdkauth.UnavailableError{Kind: kind, Err: err}
		}
		e.providerFailed(ctx, p, err)
		f.err = err
		return sdkauth.RevocationFacts{}, err
	}
	facts.CheckedAt = now
	e.providerRecovered(p)
	if encoded, err := json.Marshal(cachedRevocationFacts{Disabled: facts.Disabled, ValidAfter: facts.ValidAfter, CheckedAt: now}); err == nil {
		_ = e.cache.Set(ctx, key, encoded, e.grace)
	}
	f.facts = facts
	return facts, nil
}

// providerFailed opens an outage episode on the first failure: one ERROR line
// and one audit row per episode, never one per request.
func (e *ExternalAuthenticator) providerFailed(ctx context.Context, p externalProvider, err error) {
	e.episodeMu.Lock()
	_, open := e.episodes[p.issuer]
	if !open {
		e.episodes[p.issuer] = &revocationEpisode{start: e.now()}
	}
	e.episodeMu.Unlock()
	if open {
		return
	}
	kind := unavailableKind(err)
	e.logger.Error("external revocation provider unavailable", "provider", p.Name, "error_kind", kind)
	e.events.Record(ctx, securityEventInput{
		Type:    securityevent.TypeExternalRevocationUnavailable,
		Status:  securityevent.StatusFailure,
		Details: map[string]any{"provider": p.Name, "error_kind": kind},
	})
}

func (e *ExternalAuthenticator) providerRecovered(p externalProvider) {
	e.episodeMu.Lock()
	ep, open := e.episodes[p.issuer]
	delete(e.episodes, p.issuer)
	e.episodeMu.Unlock()
	if open {
		e.logger.Info("external revocation provider recovered", "provider", p.Name, "duration", e.now().Sub(ep.start), "affected", ep.affected)
	}
}

// outageServed counts a request answered during an episode and warns once per
// episode when stale serving or fail-open starts.
func (e *ExternalAuthenticator) outageServed(p externalProvider, mode string) {
	e.episodeMu.Lock()
	ep, open := e.episodes[p.issuer]
	if !open {
		ep = &revocationEpisode{start: e.now()}
		e.episodes[p.issuer] = ep
	}
	ep.affected++
	warn := ""
	switch {
	case mode == "stale" && !ep.staleWarned:
		ep.staleWarned, warn = true, "external revocation serving stale facts"
	case mode == "fail_open" && !ep.failOpenWarned:
		ep.failOpenWarned, warn = true, "external revocation failing open"
	}
	e.episodeMu.Unlock()
	if warn != "" {
		e.logger.Warn(warn, "provider", p.Name)
	}
}

// resolveSubject maps a verified identity to an active local user id.
func (e *ExternalAuthenticator) resolveSubject(ctx context.Context, p externalProvider, identity sdkauth.Identity) (string, error) {
	subject, err := e.subjects.Get(ctx, p.Name, identity.Subject)
	switch {
	case err == nil:
	case errors.Is(err, sdk.ErrNotFound):
		if !p.ProvisionOnFirstVerification {
			e.events.Record(ctx, securityEventInput{
				Type:    securityevent.TypeExternalSubjectUnknown,
				Status:  securityevent.StatusFailure,
				Details: map[string]any{"provider": p.Name},
			})
			return "", ErrExternalSubjectUnknown
		}
		reason, err := e.registrationRefusal(ctx, identity)
		if err != nil {
			return "", err
		}
		if reason != "" {
			e.events.Record(ctx, securityEventInput{
				Type:    securityevent.TypeExternalSubjectUnknown,
				Status:  securityevent.StatusFailure,
				Details: map[string]any{"provider": p.Name, "reason": reason},
			})
			return "", ErrExternalSubjectUnknown
		}
		if subject, err = e.provision(ctx, p, identity); err != nil {
			return "", err
		}
	default:
		return "", storeUnavailable(err)
	}
	if err := e.requireActive(ctx, p, subject.UserID); err != nil {
		return "", err
	}
	return subject.UserID, nil
}

// registrationRefusal applies the registration mode to an unknown subject
// about to be provisioned: only a provider-VERIFIED address with a pending
// invitation is admitted under invitation_only, the email first, then the
// phone, as provision picks them. An empty reason admits it.
func (e *ExternalAuthenticator) registrationRefusal(ctx context.Context, identity sdkauth.Identity) (string, error) {
	if e.registration != RegistrationInvitationOnly {
		return registrationRefusal(ctx, e.registration, e.invitations, "", "")
	}
	kind, value, verified := identifier.KindEmail, identity.Email, identity.EmailVerified
	if value == "" {
		kind, value, verified = identifier.KindPhone, identity.Phone, identity.PhoneVerified
	}
	if value == "" || !verified {
		return securityevent.ReasonInvitationRequired, nil
	}
	normalized, err := e.normalizer.Normalize(string(kind), value)
	if err != nil {
		return securityevent.ReasonInvitationRequired, nil
	}
	reason, err := registrationRefusal(ctx, e.registration, e.invitations, string(kind), normalized)
	if err != nil {
		return "", storeUnavailable(err)
	}
	return reason, nil
}

// requireActive is the local kill switch. It is never cached.
func (e *ExternalAuthenticator) requireActive(ctx context.Context, p externalProvider, userID string) error {
	u, err := e.users.Get(ctx, userID)
	switch {
	case errors.Is(err, sdk.ErrNotFound):
		return e.deny(ctx, p, userID, externalReasonInactive)
	case err != nil:
		return storeUnavailable(err)
	case u.ID != userID || !u.Active():
		return e.deny(ctx, p, userID, externalReasonInactive)
	}
	return nil
}

// provision creates the user, its notification-only identifier and the mirror
// row in one Provision call.
func (e *ExternalAuthenticator) provision(ctx context.Context, p externalProvider, identity sdkauth.Identity) (externalsubject.ExternalSubject, error) {
	if e.mirrorBroken.Load() {
		return externalsubject.ExternalSubject{}, errMirrorNotPersisted
	}
	kind, value, verified := identifier.KindEmail, identity.Email, identity.EmailVerified
	if value == "" {
		kind, value, verified = identifier.KindPhone, identity.Phone, identity.PhoneVerified
	}
	if value == "" {
		return externalsubject.ExternalSubject{}, e.deny(ctx, p, "", externalReasonNoIdentifier)
	}
	now := e.now()
	var verifiedAt time.Time
	if verified {
		verifiedAt = now
	}
	ident, err := identifier.New(e.ids, e.normalizer, "", kind, value, identifier.Uses{Notification: true}, true, verifiedAt, now)
	if err != nil {
		return externalsubject.ExternalSubject{}, e.deny(ctx, p, "", externalReasonInvalidIdentifier)
	}
	if err := e.checkCollision(ctx, p, ident); err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	email := ""
	if kind == identifier.KindEmail {
		email = ident.NormalizedValue
	}
	mirror, err := externalsubject.New(p.Name, identity.Subject, "", identity.Issuer, email, now)
	if err != nil {
		return externalsubject.ExternalSubject{}, errCredentialRejected
	}
	created, _, err := e.users.Provision(ctx, user.NewUser(e.ids, identity.DisplayName, now), ident, user.InitialCredentials{External: &mirror})
	if errors.Is(err, sdk.ErrAlreadyExists) {
		existing, getErr := e.subjects.Get(ctx, p.Name, identity.Subject)
		switch {
		case getErr == nil:
			return existing, nil
		case errors.Is(getErr, sdk.ErrNotFound):
			e.collision(ctx, p, "", kind)
			return externalsubject.ExternalSubject{}, ErrExternalSubjectCollision
		default:
			return externalsubject.ExternalSubject{}, storeUnavailable(getErr)
		}
	}
	if err != nil {
		return externalsubject.ExternalSubject{}, storeUnavailable(err)
	}
	persisted, err := e.subjects.Get(ctx, p.Name, identity.Subject)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return externalsubject.ExternalSubject{}, storeUnavailable(err)
	}
	if err != nil || persisted.UserID != created.ID {
		e.mirrorBroken.Store(true)
		e.logger.Error("user store did not persist the external subject; external provisioning disabled until restart", "provider", p.Name, "user_id", created.ID)
		return externalsubject.ExternalSubject{}, errMirrorNotPersisted
	}
	if kind == identifier.KindEmail && verified {
		resolvePendingInvitations(ctx, e.invitations, e.logger, sdk.AddressKindEmail, ident.NormalizedValue, created.ID)
	}
	e.events.Record(ctx, securityEventInput{
		UserID:  created.ID,
		Type:    securityevent.TypeExternalSubjectProvisioned,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"provider": p.Name, "identifier_kind": string(kind), "verified": verified},
	})
	return persisted, nil
}

// checkCollision refuses an address that is an ACTIVE login or recovery claim,
// whatever the token's verified flag says: it denies the presenter, never the
// account owner.
func (e *ExternalAuthenticator) checkCollision(ctx context.Context, p externalProvider, ident identifier.Identifier) error {
	for _, lookup := range []func(context.Context, string, string) (identifier.Identifier, error){e.identifiers.GetLogin, e.identifiers.GetRecovery} {
		existing, err := lookup(ctx, string(ident.Kind), ident.NormalizedValue)
		switch {
		case err == nil:
			e.collision(ctx, p, existing.UserID, ident.Kind)
			return ErrExternalSubjectCollision
		case !errors.Is(err, sdk.ErrNotFound):
			return storeUnavailable(err)
		}
	}
	return nil
}

func (e *ExternalAuthenticator) collision(ctx context.Context, p externalProvider, existingUserID string, kind identifier.Kind) {
	e.events.Record(ctx, securityEventInput{
		UserID:  existingUserID,
		Type:    securityevent.TypeExternalSubjectCollision,
		Status:  securityevent.StatusBlocked,
		Details: map[string]any{"provider": p.Name, "identifier_kind": string(kind)},
	})
}

// deny records a policy denial and returns ErrExternalSubjectDenied, or
// ErrRevoked for a revocation.
func (e *ExternalAuthenticator) deny(ctx context.Context, p externalProvider, userID, reason string) error {
	e.events.Record(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeExternalSubjectDenied,
		Status:  securityevent.StatusBlocked,
		Details: map[string]any{"provider": p.Name, "reason": reason},
	})
	if reason == externalReasonRevoked {
		return ErrRevoked
	}
	return ErrExternalSubjectDenied
}

func (c *revocationCounters) observe(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.latencies) < latencySamples {
		c.latencies = append(c.latencies, d)
		return
	}
	c.latencies[c.next] = d
	c.next = (c.next + 1) % latencySamples
}

func storeUnavailable(err error) error {
	return fmt.Errorf("%w (%s)", ErrIdentityStoreUnavailable, ErrorKind(err))
}

func unavailableKind(err error) string {
	var unavailable *sdkauth.UnavailableError
	switch {
	case errors.As(err, &unavailable):
		return string(unavailable.Kind)
	case errors.Is(err, context.DeadlineExceeded):
		return string(sdkauth.UnavailableDeadline)
	default:
		return ErrorKind(err)
	}
}

// peekIssuer reads the UNVERIFIED iss of a JWT-shaped credential within the
// sdk size cap. ok is false for anything that is not a three-segment token with
// a JSON payload; present is false when the payload has no iss.
func peekIssuer(raw string) (iss string, present, ok bool) {
	if len(raw) > sdkauth.MaxCredentialBytes || strings.Count(raw, ".") != 2 {
		return "", false, false
	}
	payload := raw[strings.IndexByte(raw, '.')+1 : strings.LastIndexByte(raw, '.')]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false, false
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return "", false, false
	}
	value, present := claims["iss"]
	if !present {
		return "", false, true
	}
	if err := json.Unmarshal(value, &iss); err != nil || iss == "" {
		return "", true, false
	}
	return iss, true, true
}
