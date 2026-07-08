package authsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/logic/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/logic/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/id"
	"github.com/gopernicus/gopernicus/sdk/identity"
)

// Principal subject-type conventions (AV5 — actor references are
// (subject_type, subject_id) string pairs, never a registry table). They alias
// the sdk/identity constants (amendment A-I1), which match the ReBAC Subject
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
// act-as-user account requires a non-empty ownerUserID (errs.ErrInvalidInput
// from construction).
func (s *Service) CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error) {
	sa, err := serviceaccount.New(name, description, createdBy, actAsUser, ownerUserID, s.now())
	if err != nil {
		return serviceaccount.ServiceAccount{}, err
	}
	return s.serviceAccounts.Create(ctx, sa)
}

// ListServiceAccounts returns a cursor-paginated page of service accounts
// (ordered created_at DESC, id DESC).
func (s *Service) ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return s.serviceAccounts.List(ctx, req)
}

// MintAPIKey generates a fresh key for serviceAccountID, persists only its
// SHA-256 hash, and returns the created record alongside the plaintext key —
// which is shown exactly once and never recoverable afterward. A zero expiresAt
// means the key never expires. An unknown service account → errs.ErrNotFound.
func (s *Service) MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error) {
	if _, err := s.serviceAccounts.Get(ctx, serviceAccountID); err != nil {
		return apikey.APIKey{}, "", err
	}
	prefix, raw := mintAPIKeySecret()
	hashed, err := s.hashAPIKey(raw)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	k, err := apikey.New(serviceAccountID, name, prefix, hashed, expiresAt, s.now())
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	created, err := s.apiKeys.Create(ctx, k)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	return created, raw, nil
}

// ListAPIKeys returns a cursor-paginated page of a service account's keys
// (ordered created_at DESC, id DESC).
func (s *Service) ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return s.apiKeys.ListByServiceAccount(ctx, serviceAccountID, req)
}

// RevokeAPIKey marks the key revoked as of now. An unknown key →
// errs.ErrNotFound.
func (s *Service) RevokeAPIKey(ctx context.Context, keyID string) error {
	return s.apiKeys.Revoke(ctx, keyID, s.now())
}

// AuthenticateAPIKey resolves the effective Principal for a raw API key. It
// hashes the key, looks it up by hash ALONE (the pinned GetByHash contract), and
// then branches in the SERVICE per design §4.1:
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
// Every deny returns the same generic errs.ErrUnauthorized so the response
// cannot distinguish unknown / revoked / expired. TouchLastUsed failures never
// fail authentication. The audit rows carry the key PREFIX only, never the raw
// key (design §5.1 WI3).
func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey string) (Principal, error) {
	if !s.MachineEnabled() {
		return Principal{}, invalidAPIKey()
	}
	hashed, err := s.hashAPIKey(rawKey)
	if err != nil {
		// An empty/invalid key never matches a stored hash.
		return Principal{}, invalidAPIKey()
	}
	key, err := s.apiKeys.GetByHash(ctx, hashed)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return Principal{}, invalidAPIKey()
		}
		return Principal{}, err
	}

	now := s.now()
	switch {
	case key.Revoked():
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusBlocked)
		return Principal{}, invalidAPIKey()
	case key.Expired(now):
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusFailure)
		return Principal{}, invalidAPIKey()
	}

	sa, err := s.serviceAccounts.Get(ctx, key.ServiceAccountID)
	if err != nil {
		return Principal{}, err
	}
	p := effectivePrincipal(sa)
	s.recordAPIKeyAuth(ctx, key, securityevent.Principal{Type: p.Type, ID: p.ID}, securityevent.StatusSuccess)
	// Best-effort: a TouchLastUsed failure must never fail authentication
	// (design §4.1). Now that the service carries a logger (A5), the previously
	// silently-swallowed error is logged at WARN with coarse fields only.
	if err := s.apiKeys.TouchLastUsed(ctx, key.ID, now); err != nil {
		s.logger.Warn("api key touch-last-used failed", "error_kind", errKind(err))
	}
	return p, nil
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

// CurrentPrincipal returns the effective Principal stashed by
// RequireServiceAccount / RequirePrincipal, if any. It is the cross-feature
// machine-or-human identity port, alongside CurrentUser.
func (s *Service) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return identity.FromContext(ctx)
}

// RequireServiceAccount gates next on an API-key bearer credential. A missing
// header, a JWT-shaped bearer (two dots), an inert subsystem, or a bad key all
// write 401. On success it stashes the resolved Principal (read via
// CurrentPrincipal) and calls next.
func (s *Service) RequireServiceAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok || isJWTToken(raw) || !s.MachineEnabled() {
			writeUnauthorized(w)
			return
		}
		p, err := s.AuthenticateAPIKey(r.Context(), raw)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
	})
}

// RequirePrincipal gates next on either credential class and stashes the
// resolved Principal (read via CurrentPrincipal). A bearer credential is classed
// by shape: exactly two dots ⇒ the JWT path (active only when a TokenSigner is
// wired — design §4.4; inert otherwise), otherwise the API-key path (active only
// when the machine repos are wired). With no bearer header it falls back to the
// session cookie → a user Principal. Any failure writes 401.
func (s *Service) RequirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.resolvePrincipal(r)
		if !ok {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
	})
}

// resolvePrincipal classes and resolves the request's credential per the §4.3
// rules. Each path is active only when its credential class is configured, so an
// unwired class denies rather than half-resolving.
func (s *Service) resolvePrincipal(r *http.Request) (Principal, bool) {
	if raw, ok := bearerToken(r); ok {
		if isJWTToken(raw) {
			// JWT bearer classing is active only when a TokenSigner is wired
			// (design §4.4). Nil → inert: a JWT bearer is never parsed
			// (deny-by-absence, A3 behavior unchanged), and it never falls
			// through to the session path. A wired-but-invalid JWT also denies.
			if s.tokenSigner == nil {
				return Principal{}, false
			}
			userID, ok := s.verifyBearer(raw)
			if !ok {
				return Principal{}, false
			}
			return Principal{Type: PrincipalUser, ID: userID}, true
		}
		if !s.MachineEnabled() {
			return Principal{}, false
		}
		p, err := s.AuthenticateAPIKey(r.Context(), raw)
		if err != nil {
			return Principal{}, false
		}
		return p, true
	}
	// No bearer credential: fall back to the session cookie → user principal.
	c, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return Principal{}, false
	}
	sess, err := s.ValidateSession(r.Context(), c.Value)
	if err != nil {
		return Principal{}, false
	}
	return Principal{Type: PrincipalUser, ID: sess.UserID}, true
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
// the full raw key `prefix_secret`. Both halves use sdk/id's dotless base32
// alphabet and are joined with `_`, so a key can NEVER contain two dots and
// collide with the §4.3 JWT-detection heuristic.
func mintAPIKeySecret() (prefix, raw string) {
	prefix = id.New()[:apiKeyPrefixLen]
	secret := id.New() + id.New()
	return prefix, prefix + "_" + secret
}

// invalidAPIKey is the single generic error every API-key denial returns, so the
// response cannot distinguish unknown / revoked / expired.
func invalidAPIKey() error {
	return fmt.Errorf("invalid api key: %w", errs.ErrUnauthorized)
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
