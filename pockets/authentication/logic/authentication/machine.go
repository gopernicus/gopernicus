package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// secrets generates the opaque random values this service mints (API-key
// prefixes and secrets, OAuth nonces, PKCE segments) with the default nanoid
// shape. Deliberately NOT the app's entity-ID strategy (WithIDs): secret
// entropy must never follow a wiring choice like sdk.DatabaseID.
var secrets = sdk.IDGenerator{}

// Principal subject-type conventions (AV5 — actor references are
// (subject_type, subject_id) string pairs, never a registry table). They alias
// the sdk constants (amendment A-I1), which match the ReBAC Subject
// vocabulary so a host's authorizer reads them unadapted.
const (
	// PrincipalUser is the subject type for a human user, and for a personal
	// (act-as-user) API key resolved to its human owner.
	PrincipalUser = sdk.PrincipalTypeUser
	// PrincipalServiceAccount is the subject type for a machine identity.
	PrincipalServiceAccount = sdk.PrincipalTypeServiceAccount
)

// apiKeyPrefixLen is the length of the displayable key prefix (stored plain).
const apiKeyPrefixLen = 8

// apiKeyNeverExpires is the api_key_minted expires_at detail of a key without
// an expiry.
const apiKeyNeverExpires = "never"

var (
	// ErrAPIKeyTTLExceeded refuses a mint whose expires_at lies beyond now plus
	// MachineConfig.APIKeyMaxTTL; the returned *APIKeyTTLExceededError carries
	// the bound. It wraps sdk.ErrInvalidInput (400).
	ErrAPIKeyTTLExceeded = fmt.Errorf("authentication: API key expires_at exceeds the maximum lifetime: %w", sdk.ErrInvalidInput)
	// ErrAPIKeyExpiryPast refuses a mint whose expires_at is at or before now.
	// It wraps sdk.ErrInvalidInput (400).
	ErrAPIKeyExpiryPast = fmt.Errorf("authentication: API key expires_at is in the past: %w", sdk.ErrInvalidInput)
	// ErrAPIKeyFilterUnsupported refuses a filtered key listing when the store
	// does not implement apikey.FilteredLister. It wraps sdk.ErrInvalidInput
	// (400): the pocket never filters a page in memory.
	ErrAPIKeyFilterUnsupported = fmt.Errorf("authentication: the API-key store does not support filtered listings: %w", sdk.ErrInvalidInput)
	// ErrServiceAccountDisabled refuses a mint for a disabled service account.
	// It wraps sdk.ErrConflict (409).
	ErrServiceAccountDisabled = fmt.Errorf("authentication: the service account is disabled: %w", sdk.ErrConflict)
	// ErrServiceAccountStatusUnavailable is returned by DisableServiceAccount
	// and EnableServiceAccount when MachineConfig.ServiceAccountStatus is not
	// wired. It wraps sdk.ErrNotFound: an unwired capability is absent.
	ErrServiceAccountStatusUnavailable = fmt.Errorf("authentication: service-account status is not wired: %w", sdk.ErrNotFound)
	// ErrServiceAccountStatusRequires is returned by New when
	// MachineConfig.ServiceAccountStatus is set without
	// Repositories.ServiceAccountStatus and the machine repositories.
	ErrServiceAccountStatusRequires = fmt.Errorf("authentication: ServiceAccountStatus requires Repositories.ServiceAccountStatus, ServiceAccounts and APIKeys: %w", sdk.ErrInvalidInput)
	// ErrServiceAccountStatusSchemaMissing is returned by New when
	// ServiceAccountStatus is wired and Repositories.ServiceAccountStatus
	// reports its schema absent: apply migration
	// 0027_service_account_status.sql. A host that does not wire it is never
	// probed.
	ErrServiceAccountStatusSchemaMissing = errors.New("authentication: ServiceAccountStatus is wired but its schema is missing (apply migration 0027_service_account_status.sql)")
)

// APIKeyTTLExceededError is ErrAPIKeyTTLExceeded with its bound: the maximum
// lifetime and the latest expires_at a mint at that instant admits.
type APIKeyTTLExceededError struct {
	MaxTTL       time.Duration
	MaxExpiresAt time.Time
}

func (e *APIKeyTTLExceededError) Error() string { return ErrAPIKeyTTLExceeded.Error() }

func (e *APIKeyTTLExceededError) Unwrap() error { return ErrAPIKeyTTLExceeded }

// Principal is the effective caller resolved from a credential (session, API
// key, or — when a TokenSigner is wired — a bearer JWT). It is a type alias for
// sdk.Principal (amendment A-I1): the single value type AV5 pins, which the
// service and host adapters share as Principal.
type Principal = sdk.Principal

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
func (s *Service) ListServiceAccounts(ctx context.Context, req list.Request) (list.Page[serviceaccount.ServiceAccount], error) {
	return s.serviceAccounts.List(ctx, req)
}

// MintAPIKey generates a fresh key for serviceAccountID, persists only its
// SHA-256 hash, and returns the created record alongside the plaintext key —
// which is shown exactly once and never recoverable afterward. A zero expiresAt
// means the key never expires, unless MachineConfig.APIKeyMaxTTL bounds it: then
// it is filled with now plus the bound, an expiresAt beyond that is
// *APIKeyTTLExceededError, and one at or before now is ErrAPIKeyExpiryPast in
// every case. An unknown service account → sdk.ErrNotFound; a disabled one,
// with ServiceAccountStatus wired, → ErrServiceAccountDisabled.
//
// A minted key records an api_key_minted audit row carrying the key PREFIX, the
// account id, expires_at (or never) and expires_at_defaulted only (design §5.1
// WI3 — never the raw key or its hash). For an act-as-user account the row's
// UserID is the human owner the key authenticates as, mirroring
// service_account_created.
func (s *Service) MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error) {
	sa, err := s.serviceAccounts.Get(ctx, serviceAccountID)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	if s.accountStatus != nil {
		status, err := s.accountStatus.GetStatus(ctx, sa.ID)
		if err != nil {
			return apikey.APIKey{}, "", storeUnavailable(err)
		}
		if status == serviceaccount.StatusDisabled {
			return apikey.APIKey{}, "", ErrServiceAccountDisabled
		}
	}
	now := s.now()
	expiresAt, defaulted, err := s.apiKeyExpiry(now, expiresAt)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	prefix, raw := mintAPIKeySecret()
	hashed, err := s.hashAPIKey(raw)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	k, err := apikey.New(s.recordIDs, serviceAccountID, name, prefix, hashed, expiresAt, now)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	created, err := s.apiKeys.Create(ctx, k)
	if err != nil {
		return apikey.APIKey{}, "", err
	}
	// A disable that committed between the status read and the insert revoked
	// every key it saw, but not this one: re-read and revoke it, so no key a
	// later enable or un-wiring would resurrect survives the race.
	if s.accountStatus != nil {
		status, err := s.accountStatus.GetStatus(ctx, sa.ID)
		if err != nil || status == serviceaccount.StatusDisabled {
			if rerr := s.apiKeys.Revoke(ctx, created.ID, s.now()); rerr != nil {
				s.logger.Error("auth: a key minted during a disable was not revoked", "error_kind", ErrorKind(rerr))
			}
			if err != nil {
				return apikey.APIKey{}, "", storeUnavailable(err)
			}
			return apikey.APIKey{}, "", ErrServiceAccountDisabled
		}
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
			"key_prefix":           created.KeyPrefix,
			"service_account_id":   serviceAccountID,
			"expires_at":           apiKeyExpiryDetail(created.ExpiresAt),
			"expires_at_defaulted": defaulted,
		},
	})
	return created, raw, nil
}

// apiKeyExpiry applies MachineConfig.APIKeyMaxTTL to a requested expiry at
// now: a past one is refused, a zero one is filled with the bound (defaulted),
// and one beyond the bound is refused with it. Without a bound a zero expiry
// stays never-expiring.
func (s *Service) apiKeyExpiry(now, expiresAt time.Time) (time.Time, bool, error) {
	if !expiresAt.IsZero() && !expiresAt.After(now) {
		return time.Time{}, false, ErrAPIKeyExpiryPast
	}
	if s.apiKeyMaxTTL <= 0 {
		return expiresAt, false, nil
	}
	latest := now.Add(s.apiKeyMaxTTL).UTC()
	if expiresAt.IsZero() {
		return latest, true, nil
	}
	if expiresAt.After(latest) {
		return time.Time{}, false, &APIKeyTTLExceededError{MaxTTL: s.apiKeyMaxTTL, MaxExpiresAt: latest}
	}
	return expiresAt, false, nil
}

// apiKeyExpiryDetail renders a key's expiry for the audit row: RFC 3339 UTC,
// or never.
func apiKeyExpiryDetail(expiresAt time.Time) string {
	if expiresAt.IsZero() {
		return apiKeyNeverExpires
	}
	return expiresAt.UTC().Format(time.RFC3339)
}

// APIKeyMaxTTL reports the resolved MachineConfig.APIKeyMaxTTL; zero is
// unlimited.
func (s *Service) APIKeyMaxTTL() time.Duration { return s.apiKeyMaxTTL }

// ListAPIKeys returns a cursor-paginated page of a service account's keys
// (ordered created_at DESC, id DESC).
func (s *Service) ListAPIKeys(ctx context.Context, serviceAccountID string, req list.Request) (list.Page[apikey.APIKey], error) {
	return s.apiKeys.ListByServiceAccount(ctx, serviceAccountID, req)
}

// APIKeyFilterSupported reports whether the key store implements
// apikey.FilteredLister.
func (s *Service) APIKeyFilterSupported() bool {
	_, ok := s.apiKeys.(apikey.FilteredLister)
	return ok
}

// ListAPIKeysFiltered is ListAPIKeys narrowed by filter in the store; a zero
// filter.Now is the service clock. An invalid filter is sdk.ErrInvalidInput; a
// store without apikey.FilteredLister is ErrAPIKeyFilterUnsupported. A key that
// expires between two pages moves between statuses: cursors do not bind the
// clock.
func (s *Service) ListAPIKeysFiltered(ctx context.Context, serviceAccountID string, filter apikey.Filter, req list.Request) (list.Page[apikey.APIKey], error) {
	if err := filter.Validate(); err != nil {
		return list.Page[apikey.APIKey]{}, err
	}
	lister, ok := s.apiKeys.(apikey.FilteredLister)
	if !ok {
		return list.Page[apikey.APIKey]{}, ErrAPIKeyFilterUnsupported
	}
	if filter.Now.IsZero() {
		filter.Now = s.now()
	}
	return lister.ListByServiceAccountFiltered(ctx, serviceAccountID, filter, req)
}

// ServiceAccountStatusEnabled reports whether MachineConfig.ServiceAccountStatus
// wired the status repository.
func (s *Service) ServiceAccountStatusEnabled() bool { return s.accountStatus != nil }

// ServiceAccountStatuses returns the statuses of ids in one read, every id
// StatusActive while the status repository is not wired.
func (s *Service) ServiceAccountStatuses(ctx context.Context, ids []string) (map[string]serviceaccount.Status, error) {
	if s.accountStatus == nil {
		out := make(map[string]serviceaccount.Status, len(ids))
		for _, id := range ids {
			out[id] = serviceaccount.StatusActive
		}
		return out, nil
	}
	return s.accountStatus.Statuses(ctx, ids)
}

// DisableServiceAccount pauses the machine identity id: in one store
// transaction it is marked disabled and every unrevoked key it holds is
// revoked, so no key of it authenticates and none is minted — and a rollback
// or un-wiring cannot resurrect the keys. A replay is Changed=false. A changed
// status records service_account_disabled with keys_revoked, attributed to
// actor. Unwired → ErrServiceAccountStatusUnavailable; unknown →
// sdk.ErrNotFound.
//
// TRUSTED: it applies NO authorization — the caller owns that decision.
func (s *Service) DisableServiceAccount(ctx context.Context, actor Principal, id string) (serviceaccount.ServiceAccount, serviceaccount.StatusChange, error) {
	return s.setServiceAccountStatus(ctx, actor, id, serviceaccount.StatusDisabled)
}

// EnableServiceAccount restores minting for the disabled machine identity id;
// the keys the disable revoked stay revoked. A changed status records
// service_account_enabled. Otherwise it is DisableServiceAccount's twin.
//
// TRUSTED: it applies NO authorization — the caller owns that decision.
func (s *Service) EnableServiceAccount(ctx context.Context, actor Principal, id string) (serviceaccount.ServiceAccount, serviceaccount.StatusChange, error) {
	return s.setServiceAccountStatus(ctx, actor, id, serviceaccount.StatusActive)
}

func (s *Service) setServiceAccountStatus(ctx context.Context, actor Principal, id string, status serviceaccount.Status) (serviceaccount.ServiceAccount, serviceaccount.StatusChange, error) {
	if s.accountStatus == nil || !s.MachineEnabled() {
		return serviceaccount.ServiceAccount{}, serviceaccount.StatusChange{}, ErrServiceAccountStatusUnavailable
	}
	sa, err := s.serviceAccounts.Get(ctx, id)
	if err != nil {
		return serviceaccount.ServiceAccount{}, serviceaccount.StatusChange{}, err
	}
	change := s.accountStatus.Enable
	eventType := securityevent.TypeServiceAccountEnabled
	if status == serviceaccount.StatusDisabled {
		change, eventType = s.accountStatus.Disable, securityevent.TypeServiceAccountDisabled
	}
	result, err := change(ctx, sa.ID, actor.Type+":"+actor.ID, s.now().UTC())
	if err != nil {
		return serviceaccount.ServiceAccount{}, serviceaccount.StatusChange{}, err
	}
	if result.Changed {
		details := map[string]any{"service_account_id": sa.ID}
		if status == serviceaccount.StatusDisabled {
			details["keys_revoked"] = result.KeysRevoked
		}
		userID := ""
		if sa.ActAsUser {
			userID = sa.OwnerUserID
		}
		s.recordSecurityEvent(ctx, securityEventInput{
			UserID:  userID,
			Actor:   securityevent.Principal{Type: actor.Type, ID: actor.ID},
			Type:    eventType,
			Status:  securityevent.StatusSuccess,
			Details: details,
		})
	}
	return sa, result, nil
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

// DeleteServiceAccount deletes the machine identity id and its API keys. With
// the store's serviceaccount.CascadeDeleter the account and every key go in one
// transaction and the counts are recorded; without it the service falls back to
// ServiceAccounts.Delete and records zero counts with cascade=false. Unknown →
// sdk.ErrNotFound.
//
// With ServiceAccountStatus wired the account's status row goes too.
//
// A deletion records one service_account_deleted row attributed to actor —
// Details carry service_account_id, key_count, unrevoked_key_count and cascade,
// never a key prefix, hash or key id. A key revoked by the deletion records no
// api_key_revoked row of its own.
//
// TRUSTED: it applies NO authorization — the caller owns that decision.
func (s *Service) DeleteServiceAccount(ctx context.Context, actor Principal, id string) (serviceaccount.Deletion, error) {
	if !s.MachineEnabled() {
		return serviceaccount.Deletion{}, fmt.Errorf("auth: machine identity is not wired: %w", sdk.ErrNotFound)
	}
	sa, err := s.serviceAccounts.Get(ctx, id)
	if err != nil {
		return serviceaccount.Deletion{}, err
	}
	var deletion serviceaccount.Deletion
	cascader, cascade := s.serviceAccounts.(serviceaccount.CascadeDeleter)
	if cascade {
		deletion, err = cascader.DeleteWithKeys(ctx, id)
	} else {
		err = s.serviceAccounts.Delete(ctx, id)
	}
	if err != nil {
		return serviceaccount.Deletion{}, err
	}
	if s.accountStatus != nil {
		if err := s.accountStatus.Delete(ctx, id); err != nil {
			s.logger.Warn("auth: service-account status row not deleted", "error_kind", ErrorKind(err))
		}
	}
	userID := ""
	if sa.ActAsUser {
		userID = sa.OwnerUserID
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Actor:  securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:   securityevent.TypeServiceAccountDeleted,
		Status: securityevent.StatusSuccess,
		Details: map[string]any{
			"service_account_id":  id,
			"key_count":           deletion.KeyCount,
			"unrevoked_key_count": deletion.UnrevokedKeyCount,
			"cascade":             cascade,
		},
	})
	return deletion, nil
}

// auditActor is the audit attribution for the machine-identity lifecycle ops:
// the principal the transport resolved from the request credential, or the zero
// Principal when a host calls the service outside a request (a job, a seeder) —
// which the rail already permits. It is deliberately NOT the createdBy argument,
// a caller-supplied string whose format varies by host.
func auditActor(ctx context.Context) securityevent.Principal {
	p, ok := sdk.PrincipalFromContext(ctx)
	if !ok {
		return securityevent.Principal{}
	}
	return securityevent.Principal{Type: p.Type, ID: p.ID}
}

// verifyAPIKeyCredential resolves a raw API key to its effective Principal and
// the Credential describing it. It hashes the key, looks it up by hash ALONE
// (the pinned GetByHash contract), and then branches in the SERVICE per design
// §4.1:
//
//   - a disabled service account, with ServiceAccountStatus wired → deny,
//     recording an apikey_auth `blocked` event with reason
//     service_account_disabled (act-as-user keys included, and the keys the
//     disable revoked);
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
// unknown / revoked / expired / disabled, and a repository error denies rather
// than admitting — except an unreadable status, which is a non-nil error
// matching sdk.ErrUnavailable (503), so an outage never reads as a bad key.
// TouchLastUsed failures never fail authentication. The audit rows
// carry the key PREFIX only, never the raw key (design §5.1 WI3). It is the
// pocket's ONLY raw-key verification path: raw credential verification is not an
// application-service entry point, so it is reached exclusively through
// the inbound authenticator.
func (s *Service) verifyAPIKeyCredential(ctx context.Context, rawKey string) (Principal, Credential, bool, error) {
	hashed, err := s.hashAPIKey(rawKey)
	if err != nil {
		// An empty/invalid key never matches a stored hash.
		return Principal{}, Credential{}, false, nil
	}
	key, err := s.apiKeys.GetByHash(ctx, hashed)
	if err != nil {
		return Principal{}, Credential{}, false, nil
	}

	// The account's status is read first, so a key the disable revoked is
	// attributed to the disable rather than to a bare revocation.
	if s.accountStatus != nil {
		status, err := s.accountStatus.GetStatus(ctx, key.ServiceAccountID)
		if err != nil {
			return Principal{}, Credential{}, false, storeUnavailable(err)
		}
		if status == serviceaccount.StatusDisabled {
			s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusBlocked, securityevent.ReasonServiceAccountDisabled)
			return Principal{}, Credential{}, false, nil
		}
	}
	now := s.now()
	switch {
	case key.Revoked():
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusBlocked, "")
		return Principal{}, Credential{}, false, nil
	case key.Expired(now):
		s.recordAPIKeyAuth(ctx, key, saPrincipal(key.ServiceAccountID), securityevent.StatusFailure, "")
		return Principal{}, Credential{}, false, nil
	}

	sa, err := s.serviceAccounts.Get(ctx, key.ServiceAccountID)
	if err != nil {
		return Principal{}, Credential{}, false, nil
	}
	p := effectivePrincipal(sa)
	// An act-as-user key authenticates AS a human subject, so the subject's
	// lifecycle status governs it exactly as it governs a login.
	// Without this, deactivating a user would leave every act-as-user key still
	// acting as them — a live credential the admin console believes it revoked.
	// A service-account principal has no user subject and is unaffected.
	if p.Type == PrincipalUser {
		deactivated, err := s.userDeactivated(ctx, p.ID)
		if err != nil {
			// Fail closed: an unreadable subject is not proof of an active one.
			return Principal{}, Credential{}, false, nil
		}
		if deactivated {
			s.recordAPIKeyAuth(ctx, key, securityevent.Principal{Type: p.Type, ID: p.ID}, securityevent.StatusBlocked, "")
			return Principal{}, Credential{}, false, nil
		}
	}
	s.recordAPIKeyAuth(ctx, key, securityevent.Principal{Type: p.Type, ID: p.ID}, securityevent.StatusSuccess, "")
	// Best-effort: a TouchLastUsed failure must never fail authentication
	// (design §4.1). Now that the service carries a logger (A5), the previously
	// silently-swallowed error is logged at WARN with coarse fields only.
	if err := s.apiKeys.TouchLastUsed(ctx, key.ID, now); err != nil {
		s.logger.Warn("api key touch-last-used failed", "error_kind", ErrorKind(err))
	}
	return p, Credential{
		Kind:             CredentialAPIKey,
		Transport:        TransportHeader,
		APIKeyID:         key.ID,
		ServiceAccountID: sa.ID,
		ActAsUser:        sa.ActAsUser,
	}, true, nil
}

// recordAPIKeyAuth appends an apikey_auth audit row attributed to actor. Details
// carries the key PREFIX only (never the raw key or its hash — design §5.1 WI3)
// and, when one is given, a bounded reason; for a denied attempt the actor is
// the owning service account, so a blocked key is traceable even though the
// credential itself is rejected.
func (s *Service) recordAPIKeyAuth(ctx context.Context, key apikey.APIKey, actor securityevent.Principal, status, reason string) {
	details := map[string]any{"key_prefix": key.KeyPrefix}
	if reason != "" {
		details["reason"] = reason
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		Actor:   actor,
		Type:    securityevent.TypeAPIKeyAuth,
		Status:  status,
		Details: details,
	})
}

// RecordActAsUserKeyRefused records the blocked apikey_auth row of an
// act-as-user API key the inbound authenticator resolved and then refused on
// group, the surface that refused it. The resolution-time success row stays as
// it is; this row names the key and its account by id only — never the key, its
// prefix or its hash — with the service account as Actor, and is Budgeted per
// client IP because a key holder can drive it. A credential that is not an
// act-as-user API key records nothing.
func (s *Service) RecordActAsUserKeyRefused(ctx context.Context, cred Credential, group string) {
	if cred.Kind != CredentialAPIKey || !cred.ActAsUser {
		return
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		Actor:  saPrincipal(cred.ServiceAccountID),
		Type:   securityevent.TypeAPIKeyAuth,
		Status: securityevent.StatusBlocked,
		Details: map[string]any{
			"reason":             securityevent.ReasonActAsUserRefused,
			"group":              group,
			"api_key_id":         cred.APIKeyID,
			"service_account_id": cred.ServiceAccountID,
		},
		Budgeted: true,
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
	return sdk.PrincipalFromContext(ctx)
}

// verifyAccessTokenCredential verifies an access JWT presented over transport and builds
// its Principal + Credential. The signer is always wired (D3), so verification
// covers signature + expiry. First-party session_id remains unproven until a
// Live() gate; Authenticate always checks delegated session liveness.
func (s *Service) verifyAccessTokenCredential(raw string, transport Transport) (Principal, Credential, bool) {
	userID, cred, ok := s.verifyAccessClaims(raw)
	if !ok {
		return Principal{}, Credential{}, false
	}
	if cred.Profile == session.ProfileDelegated && (transport != TransportHeader || s.delegatedTokens.Issuer == "" || cred.Issuer != s.delegatedTokens.Issuer) {
		return Principal{}, Credential{}, false
	}
	cred.Transport = transport
	return Principal{Type: PrincipalUser, ID: userID}, cred, true
}

// enforceCredentialLiveness applies tier to an already-resolved credential and
// returns the context a passing request continues with (§1.4):
//
//   - external token: delegated to the external engine FIRST, so the
//     access-token early return below can never pass it unchecked;
//   - access token: one PK lookup on the session row, stamping the proven id so a
//     sensitive-mutation handler binds its step-up grant to that exact session; a
//     missing or expired row denies, an unreadable one is sdk.ErrUnavailable —
//     both fail CLOSED (D1). LivenessStateless and safe reads under
//     LivenessOnMutation skip the read for first-party tokens;
//   - API key: already DB-checked at resolution and owning no session row, it
//     passes without another lookup;
//   - a first-party session already proven by an outer gate reuses that read.
//     Delegated credentials always re-read, whatever the tier; a transport may
//     reuse the context for its next operation after the connection has been revoked.
func (s *Service) enforceCredentialLiveness(ctx context.Context, cred Credential, tier LivenessTier, unsafe bool) (context.Context, error) {
	if cred.Kind == CredentialExternalToken {
		proof, ok := s.currentProof(ctx)
		if !ok || s.external == nil {
			return clearCredential(ctx), errNotLive
		}
		return s.external.requireLiveProof(ctx, proof, tier, unsafe)
	}
	if cred.Kind != CredentialAccessToken {
		return ctx, nil
	}
	if cred.Profile == session.ProfileFirstParty && (tier == LivenessStateless || (tier == LivenessOnMutation && !unsafe)) {
		return ctx, nil
	}
	if id, proven := s.CurrentSessionID(ctx); cred.Profile == session.ProfileFirstParty && proven && id == cred.SessionID {
		return ctx, nil
	}
	proof, ok := s.currentProof(ctx)
	if !ok {
		return clearCredential(ctx), errNotLive
	}
	if err := s.credentialSessionLive(ctx, proof.principal, cred); err != nil {
		return clearCredential(ctx), err
	}
	return s.withSessionID(ctx, cred.SessionID), nil
}

func (s *Service) credentialSessionLive(ctx context.Context, principal Principal, cred Credential) error {
	if s.sessions == nil || cred.SessionID == "" || principal.Type != PrincipalUser || principal.ID == "" {
		return errNotLive
	}
	sess, err := s.sessions.Get(ctx, cred.SessionID)
	if err != nil {
		return liveReadError(ctx, err)
	}
	if sess.ID != cred.SessionID || sess.UserID != principal.ID || sess.Expired(s.now()) {
		return errNotLive
	}
	if cred.Profile == session.ProfileFirstParty {
		if !sess.FirstParty() {
			return errNotLive
		}
		return nil
	}
	if cred.Profile != session.ProfileDelegated || sess.Profile != session.ProfileDelegated || !s.now().Before(cred.accessExpiresAt) || s.users == nil || !s.delegationMatches(sess.Delegation, cred) {
		return errNotLive
	}
	u, err := s.users.Get(ctx, principal.ID)
	if err != nil {
		return liveReadError(ctx, err)
	}
	if u.ID != principal.ID || !u.Active() || ctx.Err() != nil {
		return errNotLive
	}
	return nil
}

// liveReadError keeps a missing row a denial and makes an unreadable one
// sdk.ErrUnavailable, so a store outage is never reported as a revoked credential.
func liveReadError(ctx context.Context, err error) error {
	if errors.Is(err, sdk.ErrNotFound) || ctx.Err() != nil {
		return errNotLive
	}
	return storeUnavailable(err)
}

func (s *Service) delegationMatches(binding session.Delegation, cred Credential) bool {
	if s.delegatedTokens.Issuer == "" || cred.Issuer != s.delegatedTokens.Issuer || binding.Issuer != cred.Issuer || len(cred.Audiences) != 1 {
		return false
	}
	for _, value := range []string{binding.ClientID, binding.Resource, binding.ExchangeResource, binding.ExchangeClientID} {
		if _, ok := claimString(value); !ok {
			return false
		}
	}
	if binding.Resource == binding.ExchangeResource || binding.ClientID == binding.ExchangeClientID {
		return false
	}
	if cred.ActorID == "" && cred.OriginClientID == "" {
		return cred.ClientID == binding.ClientID && cred.Audiences[0] == binding.Resource
	}
	return cred.ClientID == binding.ExchangeClientID && cred.ActorID == binding.ExchangeClientID && cred.OriginClientID == binding.ClientID && cred.Audiences[0] == binding.ExchangeResource
}

// hashAPIKey returns the stored form of a raw API key — its SHA-256 hex digest
// (cryptids.SHA256, the same primitive used for session tokens). An empty
// key is rejected.
func (s *Service) hashAPIKey(raw string) (string, error) {
	return cryptids.SHA256(raw)
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
// the full raw key `prefix_secret`. Both halves use sdk' dotless
// alphabet and are joined with `_`, so a key can NEVER contain two dots and
// collide with the §4.3 JWT-detection heuristic.
func mintAPIKeySecret() (prefix, raw string) {
	prefix = secrets.MustGenerate()[:apiKeyPrefixLen]
	secret := secrets.MustGenerate() + secrets.MustGenerate()
	return prefix, prefix + "_" + secret
}
