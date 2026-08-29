package authsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// secrets generates the opaque random values this service mints (API-key
// prefixes and secrets, OAuth nonces, PKCE segments) with the default nanoid
// shape. Deliberately NOT the app's entity-ID strategy (Deps.IDs): secret
// entropy must never follow a wiring choice like cryptids.Database.
var secrets = cryptids.IDGenerator{}

// Principal subject-type conventions (AV5 — actor references are
// (subject_type, subject_id) string pairs, never a registry table). They alias
// the sdk/foundation/identity constants (amendment A-I1), which match the ReBAC Subject
// vocabulary so a host's authorizer reads them unadapted.
const (
	// PrincipalUser is the subject type for a human user, and for a personal
	// (act-as-user) API key resolved to its human owner.
	PrincipalUser = identity.User
	// PrincipalServiceAccount is the subject type for a machine identity.
	PrincipalServiceAccount = identity.ServiceAccount
)

// apiKeyPrefixLen is the length of the displayable key prefix (stored plain).
const apiKeyPrefixLen = 8

// Principal is the effective caller resolved from a credential (session, API
// key, or — when a TokenSigner is wired — a bearer JWT). It is a type alias for
// identity.Principal (amendment A-I1): the single value type AV5 pins, which the
// public auth package re-exports as auth.Principal.
type Principal = identity.Principal

// MachineEnabled reports whether the API-key / service-account subsystem is
// wired. The transport registers the machine lifecycle routes only when it is
// true (deny-by-absence, design §4.1), and the bearer API-key path is inert
// otherwise.
func (s *Service) MachineEnabled() bool {
	return s.apiKeys != nil && s.serviceAccounts != nil
}

// CreateServiceAccount persists a new machine identity created by createdBy. An
// act-as-user account requires a non-empty ownerUserID (sdk.ErrInvalidInput
// from construction) naming an EXISTING user: an act-as-user key authenticates
// as its owner, so an unknown owner would mint a live principal for a subject
// nobody can deactivate (userDeactivated reads an unknown id as active). An
// unknown owner is sdk.ErrInvalidReference; an unwired user rail fails closed
// with ErrIdentityUnavailable.
//
// A created account records a service_account_created audit row attributed to
// the principal in ctx — never to createdBy, which is a caller-supplied string.
func (s *Service) CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error) {
	sa, err := serviceaccount.New(s.ids, name, description, createdBy, actAsUser, ownerUserID, s.now())
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	// The persisted (trimmed) owner is the one validated, audited, and stored, so
	// the existence check and the row can never disagree about who the owner is.
	if actAsUser {
		if s.users == nil {
			return serviceaccount.ServiceAccount{}, ErrIdentityUnavailable
		}
		if _, err := s.users.Get(ctx, sa.OwnerUserID); err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				return serviceaccount.ServiceAccount{}, fmt.Errorf("act-as owner %q: %w", sa.OwnerUserID, sdk.ErrInvalidReference)
			}
			return serviceaccount.ServiceAccount{}, err
		}
	}
	created, err := s.serviceAccounts.Create(ctx, sa)
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	userID := ""
	if actAsUser {
		userID = created.OwnerUserID
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Actor:  auditActor(ctx),
		Type:   securityevent.TypeServiceAccountCreated,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"service_account_id": created.ID,
			"act_as_user":        actAsUser,
			// delegated marks an act-as account whose owner is someone other than
			// its creator — the impersonation-shaped case the gate exists for.
			"delegated": actAsUser && created.OwnerUserID != created.CreatedBy,
		},
	})
	return created, nil
}

// ListServiceAccounts returns a cursor-paginated page of service accounts
// (ordered created_at DESC, id DESC).
func (s *Service) ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return s.serviceAccounts.List(ctx, req)
}

// MintAPIKey generates a fresh key for serviceAccountID, persists only its
// SHA-256 hash, and returns the created record alongside the plaintext key —
// which is shown exactly once and never recoverable afterward. A zero expiresAt
// means the key never expires. An unknown service account → sdk.ErrNotFound.
//
// A minted key records an api_key_minted audit row carrying the key PREFIX and
// the account id only (design §5.1 WI3 — never the raw key or its hash). For an
// act-as-user account the row's UserID is the human owner the key authenticates
// as, mirroring service_account_created.
func (s *Service) MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error) {
	sa, err := s.serviceAccounts.Get(ctx, serviceAccountID)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	prefix, raw := mintAPIKeySecret()
	hashed, err := s.hashAPIKey(raw)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	k, err := apikey.New(s.ids, serviceAccountID, name, prefix, hashed, expiresAt, s.now())
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	created, err := s.apiKeys.Create(ctx, k)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	// An act-as-user key authenticates as a HUMAN, so the row names that human as
	// its subject — the same UserID convention service_account_created follows.
	userID := ""
	if sa.ActAsUser {
		userID = sa.OwnerUserID
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Actor:  auditActor(ctx),
		Type:   securityevent.TypeAPIKeyMinted,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"key_prefix":         created.KeyPrefix,
			"service_account_id": serviceAccountID,
		},
	})
	return created, raw, nil
}

// ListAPIKeys returns a cursor-paginated page of a service account's keys
// (ordered created_at DESC, id DESC).
func (s *Service) ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return s.apiKeys.ListByServiceAccount(ctx, serviceAccountID, req)
}

// RevokeAPIKey marks the key revoked as of now. An unknown key →
// sdk.ErrNotFound. A revoked key records an api_key_revoked audit row carrying
// the key id (the only coordinate the revoke path holds). The row is written per
// successful CALL, not per state transition: re-revoking an already-revoked key
// records a second row, because the revoke path has no Get-by-id with which to
// read the prior state.
func (s *Service) RevokeAPIKey(ctx context.Context, keyID string) error {
	if err := s.apiKeys.Revoke(ctx, keyID, s.now()); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		Actor:   auditActor(ctx),
		Type:    securityevent.TypeAPIKeyRevoked,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"key_id": keyID},
	})
	return nil
}

// auditActor is the audit attribution for the machine-identity lifecycle ops:
// the principal the transport resolved from the request credential, or the zero
// Principal when a host calls the service outside a request (a job, a seeder) —
// which the rail already permits. It is deliberately NOT the createdBy argument,
// a caller-supplied string whose format varies by host.
func auditActor(ctx context.Context) securityevent.Principal {
	p, ok := identity.FromContext(ctx)
	if !ok {
		return securityevent.Principal{}
	}
	return securityevent.Principal{Type: p.Type, ID: p.ID}
}

// resolveAPIKeyCredential resolves a raw API key to its effective Principal and
// the Credential describing it. It hashes the key, looks it up by hash ALONE
// (the pinned GetByHash contract), and then branches in the SERVICE per design
// §4.1:
//
//   - revoked → deny, recording an apikey_auth `blocked` event WITH
//     service-account attribution (key.ServiceAccountID — exactly why the pinned
//     GetByHash contract returns revoked rows);
//   - expired (a set ExpiresAt in the past) → deny, recording an apikey_auth
//     `failure` event;
//   - valid → resolve the effective principal (ActAsUser → the human owner, else
//     the service account itself), record an apikey_auth `success` event, and
//     best-effort touch LastUsedAt.
//
// Every denial is the same opaque false, so a response can never distinguish
// unknown / revoked / expired, and a repository error denies rather than
// admitting. TouchLastUsed failures never fail authentication. The audit rows
// carry the key PREFIX only, never the raw key (design §5.1 WI3). It is the
// pocket's ONLY raw-key verification path: raw credential verification is not an
// application-service entry point, so it is reached exclusively through
// RequirePrincipal.
func (s *Service) resolveAPIKeyCredential(ctx context.Context, rawKey string) (Principal, Credential, bool) {
	hashed, err := s.hashAPIKey(rawKey)
	if err != nil {
		// An empty/invalid key never matches a stored hash.
		return Principal{}, Credential{}, false
	}
	key, err := s.apiKeys.GetByHash(ctx, hashed)
	if err != nil {
		return Principal{}, Credential{}, false
	}

	now := s.now()
	switch {
	case key.Revoked():
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusBlocked)
		return Principal{}, Credential{}, false
	case key.Expired(now):
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusFailure)
		return Principal{}, Credential{}, false
	}

	sa, err := s.serviceAccounts.Get(ctx, key.ServiceAccountID)
	if err != nil {
		return Principal{}, Credential{}, false
	}
	p := effectivePrincipal(sa)
	// An act-as-user key authenticates AS a human subject, so the subject's
	// lifecycle status governs it exactly as it governs a login (CHAU-1.5).
	// Without this, deactivating a user would leave every act-as-user key still
	// acting as them — a live credential the admin console believes it revoked.
	// A service-account principal has no user subject and is unaffected.
	if p.Type == PrincipalUser {
		deactivated, err := s.userDeactivated(ctx, p.ID)
		if err != nil {
			// Fail closed: an unreadable subject is not proof of an active one.
			return Principal{}, Credential{}, false
		}
		if deactivated {
			s.recordAPIKeyAuth(ctx, key, securityevent.Principal{Type: p.Type, ID: p.ID}, securityevent.StatusBlocked)
			return Principal{}, Credential{}, false
		}
	}
	s.recordAPIKeyAuth(ctx, key, securityevent.Principal{Type: p.Type, ID: p.ID}, securityevent.StatusSuccess)
	// Best-effort: a TouchLastUsed failure must never fail authentication
	// (design §4.1). Now that the service carries a logger (A5), the previously
	// silently-swallowed error is logged at WARN with coarse fields only.
	if err := s.apiKeys.TouchLastUsed(ctx, key.ID, now); err != nil {
		s.logger.Warn("api key touch-last-used failed", "error_kind", errKind(err))
	}
	return p, Credential{
		Kind:             CredentialAPIKey,
		Transport:        TransportHeader,
		APIKeyID:         key.ID,
		ServiceAccountID: sa.ID,
		ActAsUser:        sa.ActAsUser,
	}, true
}

// recordAPIKeyAuth appends an apikey_auth audit row attributed to actor. Details
// carries the key PREFIX only (never the raw key or its hash — design §5.1 WI3);
// for a denied attempt the actor is the owning service account, so a blocked
// key is traceable even though the credential itself is rejected.
func (s *Service) recordAPIKeyAuth(ctx context.Context, key apikey.APIKey, actor securityevent.Principal, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		Actor:   actor,
		Type:    securityevent.TypeAPIKeyAuth,
		Status:  status,
		Details: map[string]any{"key_prefix": key.KeyPrefix},
	})
}

// saPrincipal builds the service-account attribution for a key whose owning
// account was not (or need not be) resolved — the denied-key audit path.
func saPrincipal(serviceAccountID string) securityevent.Principal {
	return securityevent.Principal{Type: PrincipalServiceAccount, ID: serviceAccountID}
}

// CurrentPrincipal returns the effective Principal stashed by RequirePrincipal,
// if any. It is the cross-pocket machine-or-human identity port, alongside
// CurrentUser and CurrentCredential.
func (s *Service) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return identity.FromContext(ctx)
}

// RequirePrincipal is THE authenticator. Its options are OR-sets over credential
// kinds (Accept) and transports (Transports) plus a liveness tier (Live) and a
// browser denial mode (Browser); with no options it admits every wired
// credential over both transports, statelessly, denying with a JSON 401.
//
// At the OUTERMOST position it resolves the request's credential within its set
// (resolveCredential) and stashes the Principal (read via CurrentPrincipal /
// CurrentUser) plus the Credential (read via CurrentCredential). NESTED under an
// outer RequirePrincipal it never re-resolves: it narrows, checking the stashed
// Credential against its own set and denying when it falls outside. A Live()
// gate runs the session lookup once — a nested Live() under an outer one reads
// the proven CurrentSessionID and passes.
//
// The set is resolved at CONSTRUCTION, so an empty Accept()/Transports() panics
// at wiring time. Nested narrowing trusts the stash written earlier in the same
// chain: the supported wiring invariant is ONE authentication Service per chain.
func (s *Service) RequirePrincipal(opts ...PrincipalOption) web.Middleware {
	set := s.resolveSet(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cred, nested := currentCredential(ctx)
			if nested {
				if !set.admits(cred.Kind, cred.Transport) {
					s.denyPrincipal(w, r, set)
					return
				}
			} else {
				p, resolved, ok := s.resolveCredential(r, set)
				if !ok {
					s.denyPrincipal(w, r, set)
					return
				}
				cred = resolved
				ctx = withCredential(identity.WithPrincipal(ctx, p), cred)
			}
			if set.live {
				live, ok := s.enforceLive(ctx, cred)
				if !ok {
					s.denyPrincipal(w, r, set)
					return
				}
				ctx = live
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveCredential resolves the request's credential WITHIN set — the one
// resolver, at the outermost position (design §4.3):
//
//   - a bearer header, when the header transport is in the set, is AUTHORITATIVE:
//     it is classed by shape (isJWTToken — exactly two dots ⇒ access token, else
//     ⇒ API key) and a failure denies; the cookie is never read after one;
//   - otherwise the access-JWT session cookie, when the cookie transport is in
//     the set;
//   - otherwise no credential at all.
//
// A credential arriving on a transport OUTSIDE the set is ignored, not denied:
// the set says what the surface reads, so a never-consulted header is not a
// bypass. A credential whose KIND is outside the set denies.
func (s *Service) resolveCredential(r *http.Request, set principalSet) (Principal, Credential, bool) {
	if set.header {
		if raw, ok := bearerToken(r); ok {
			if isJWTToken(raw) {
				return s.resolveAccessToken(raw, TransportHeader, set)
			}
			// The API-key path is active only when the machine subsystem is wired
			// (deny-by-absence) and the surface admits keys.
			if !set.apiKey || !s.MachineEnabled() {
				return Principal{}, Credential{}, false
			}
			return s.resolveAPIKeyCredential(r.Context(), raw)
		}
	}
	if set.cookie {
		if c, err := r.Cookie(s.cookie.Name); err == nil {
			return s.resolveAccessToken(c.Value, TransportCookie, set)
		}
	}
	return Principal{}, Credential{}, false
}

// resolveAccessToken verifies an access JWT presented over transport and builds
// its Principal + Credential. The signer is always wired (D3), so verification
// covers signature + expiry; the session_id claim rides along unproven until a
// Live() gate looks it up.
func (s *Service) resolveAccessToken(raw string, transport Transport, set principalSet) (Principal, Credential, bool) {
	if !set.accessToken {
		return Principal{}, Credential{}, false
	}
	userID, sessionID, ok := s.verifyBearerClaims(raw)
	if !ok {
		return Principal{}, Credential{}, false
	}
	return Principal{Type: PrincipalUser, ID: userID},
		Credential{Kind: CredentialAccessToken, Transport: transport, SessionID: sessionID},
		true
}

// enforceLive applies the Live() tier to an already-resolved credential and
// returns the context a passing request continues with (§1.4):
//
//   - access token: one PK lookup on the session row, stamping the proven id so a
//     sensitive-mutation handler binds its step-up grant to that exact session; a
//     missing, expired, or unreadable row denies — fails CLOSED (D1);
//   - API key: already DB-checked at resolution and owning no session row, it
//     passes without another lookup;
//   - a session id already proven by an OUTER Live() passes without a second
//     lookup.
func (s *Service) enforceLive(ctx context.Context, cred Credential) (context.Context, bool) {
	if cred.Kind != CredentialAccessToken {
		return ctx, true
	}
	if _, proven := s.CurrentSessionID(ctx); proven {
		return ctx, true
	}
	if !s.sessionLive(ctx, cred.SessionID) {
		return ctx, false
	}
	return withSessionID(ctx, cred.SessionID), true
}

// denyPrincipal writes an authenticator's denial: the byte-stable JSON 401, or —
// for a Browser() set — the 303 to the configured browser login path carrying a
// validated return_to on GET/HEAD (design §9.2). A nested authenticator denies in
// its OWN mode, so a plain helper nested under a browser gate answers JSON.
func (s *Service) denyPrincipal(w http.ResponseWriter, r *http.Request, set principalSet) {
	if set.browser {
		s.redirectToBrowserLogin(w, r)
		return
	}
	writeUnauthorized(w)
}

// hashAPIKey returns the stored form of a raw API key — its SHA-256 hex digest
// (cryptids.SHA256Hasher, the same primitive used for session tokens). An empty
// key is rejected.
func (s *Service) hashAPIKey(raw string) (string, error) {
	return s.tokenHasher.Hash(raw)
}

// effectivePrincipal resolves a service account to its effective caller: an
// act-as-user account resolves to its human owner, otherwise to the account
// itself (design §4.1).
func effectivePrincipal(sa serviceaccount.ServiceAccount) Principal {
	if sa.ActAsUser {
		return Principal{Type: PrincipalUser, ID: sa.OwnerUserID}
	}
	return Principal{Type: PrincipalServiceAccount, ID: sa.ID}
}

// mintAPIKeySecret builds a fresh key: a displayable prefix (stored plain) and
// the full raw key `prefix_secret`. Both halves use sdk/foundation/cryptids' dotless
// alphabet and are joined with `_`, so a key can NEVER contain two dots and
// collide with the §4.3 JWT-detection heuristic.
func mintAPIKeySecret() (prefix, raw string) {
	prefix = secrets.MustGenerate()[:apiKeyPrefixLen]
	secret := secrets.MustGenerate() + secrets.MustGenerate()
	return prefix, prefix + "_" + secret
}

// bearerToken extracts the token from an `Authorization: Bearer <token>` header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// isJWTToken reports whether a bearer token is JWT-shaped: a JWT has exactly two
// dots (header.payload.signature), and a dotless API key never does (design
// §4.3's classing heuristic).
func isJWTToken(token string) bool {
	return strings.Count(token, ".") == 2
}
