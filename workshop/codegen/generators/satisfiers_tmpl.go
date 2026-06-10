package generators

// satisfierSources mirrors the framework's hand-written satisfier sources
// under core/auth/*/satisfiers, keyed by project-relative path. The emitted
// project files are these sources verbatim, with core/repositories imports
// rewritten to the project module and the generated header prepended (see
// emitSatisfiers). Drift between this snapshot and the framework sources is
// pinned by TestSatisfierSourcesMatchFramework.
var satisfierSources = map[string]string{
	"core/auth/authentication/satisfiers/api_keys.go": `package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/apikeys"
)

var _ authentication.APIKeyRepository = (*APIKeySatisfier)(nil)

type apiKeyRepo interface {
	GetByHash(ctx context.Context, keyHash string, now time.Time) (apikeys.APIKey, error)
}

// APIKeySatisfier satisfies authentication.APIKeyRepository using the generated api_keys repository.
type APIKeySatisfier struct {
	repo apiKeyRepo
}

func NewAPIKeySatisfier(repo apiKeyRepo) *APIKeySatisfier {
	return &APIKeySatisfier{repo: repo}
}

func (s *APIKeySatisfier) GetByHash(ctx context.Context, hash string) (authentication.APIKey, error) {
	ak, err := s.repo.GetByHash(ctx, hash, time.Now().UTC())
	if err != nil {
		return authentication.APIKey{}, err
	}
	return authentication.APIKey{
		ID:               ak.APIKeyID,
		ServiceAccountID: ak.ParentServiceAccountID,
		KeyHash:          ak.KeyHash,
		ExpiresAt:        ak.ExpiresAt,
		Active:           ak.RecordState == "active",
	}, nil
}
`,
	"core/auth/authentication/satisfiers/oauth_accounts.go": `package satisfiers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/oauthaccounts"
)

var _ authentication.OAuthAccountRepository = (*OAuthAccountSatisfier)(nil)

type oauthAccountRepo interface {
	GetByProvider(ctx context.Context, provider string, providerUserID string) (oauthaccounts.OauthAccount, error)
	ListByUser(ctx context.Context, parentUserID string, limit int) ([]oauthaccounts.OauthAccount, error)
	Create(ctx context.Context, input oauthaccounts.CreateOauthAccount) (oauthaccounts.OauthAccount, error)
	DeleteByUserAndProvider(ctx context.Context, parentUserID string, provider string) error
}

// OAuthAccountSatisfier satisfies authentication.OAuthAccountRepository
// using the generated oauth_accounts repository.
type OAuthAccountSatisfier struct {
	repo oauthAccountRepo
}

func NewOAuthAccountSatisfier(repo oauthAccountRepo) *OAuthAccountSatisfier {
	return &OAuthAccountSatisfier{repo: repo}
}

func (s *OAuthAccountSatisfier) GetByProvider(ctx context.Context, provider, providerUserID string) (authentication.OAuthAccount, error) {
	oa, err := s.repo.GetByProvider(ctx, provider, providerUserID)
	if err != nil {
		return authentication.OAuthAccount{}, err
	}
	return toAuthOAuthAccount(oa), nil
}

func (s *OAuthAccountSatisfier) Create(ctx context.Context, account authentication.OAuthAccount) error {
	var profileData *json.RawMessage
	if account.ProfileData != nil {
		raw := json.RawMessage(account.ProfileData)
		profileData = &raw
	}
	_, err := s.repo.Create(ctx, oauthaccounts.CreateOauthAccount{
		ParentUserID:          account.UserID,
		Provider:              account.Provider,
		ProviderUserID:        account.ProviderUserID,
		ProviderEmail:         strPtr(account.ProviderEmail),
		ProviderEmailVerified: &account.ProviderEmailVerified,
		AccountVerified:       account.AccountVerified,
		AccessToken:           strPtr(account.AccessToken),
		RefreshToken:          strPtr(account.RefreshToken),
		TokenExpiresAt:        timePtr(account.TokenExpiresAt),
		TokenType:             strPtr(account.TokenType),
		Scope:                 strPtr(account.Scope),
		IDToken:               strPtr(account.IDToken),
		ProfileData:           profileData,
		LinkedAt:              account.LinkedAt,
	})
	return err
}

func (s *OAuthAccountSatisfier) GetByUserID(ctx context.Context, userID string) ([]authentication.OAuthAccount, error) {
	accounts, err := s.repo.ListByUser(ctx, userID, 20)
	if err != nil {
		return nil, err
	}
	result := make([]authentication.OAuthAccount, len(accounts))
	for i, oa := range accounts {
		result[i] = toAuthOAuthAccount(oa)
	}
	return result, nil
}

func (s *OAuthAccountSatisfier) Delete(ctx context.Context, userID, provider string) error {
	return s.repo.DeleteByUserAndProvider(ctx, userID, provider)
}

func toAuthOAuthAccount(oa oauthaccounts.OauthAccount) authentication.OAuthAccount {
	a := authentication.OAuthAccount{
		UserID:          oa.ParentUserID,
		Provider:        oa.Provider,
		ProviderUserID:  oa.ProviderUserID,
		AccountVerified: oa.AccountVerified,
		LinkedAt:        oa.LinkedAt,
	}
	if oa.ProviderEmail != nil {
		a.ProviderEmail = *oa.ProviderEmail
	}
	if oa.ProviderEmailVerified != nil {
		a.ProviderEmailVerified = *oa.ProviderEmailVerified
	}
	if oa.AccessToken != nil {
		a.AccessToken = *oa.AccessToken
	}
	if oa.RefreshToken != nil {
		a.RefreshToken = *oa.RefreshToken
	}
	if oa.TokenExpiresAt != nil {
		a.TokenExpiresAt = *oa.TokenExpiresAt
	}
	if oa.TokenType != nil {
		a.TokenType = *oa.TokenType
	}
	if oa.Scope != nil {
		a.Scope = *oa.Scope
	}
	if oa.IDToken != nil {
		a.IDToken = *oa.IDToken
	}
	if oa.ProfileData != nil {
		a.ProfileData = json.RawMessage(*oa.ProfileData)
	}
	return a
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
`,
	"core/auth/authentication/satisfiers/passwords.go": `package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/userpasswords"
)

var _ authentication.PasswordRepository = (*PasswordSatisfier)(nil)

type passwordRepo interface {
	Get(ctx context.Context, userID string) (userpasswords.UserPassword, error)
	Create(ctx context.Context, input userpasswords.CreateUserPassword) (userpasswords.UserPassword, error)
	Update(ctx context.Context, userID string, input userpasswords.UpdateUserPassword) (userpasswords.UserPassword, error)
	Delete(ctx context.Context, userID string) error
	SetVerified(ctx context.Context, updatedAt time.Time, userID string) error
}

// PasswordSatisfier satisfies authentication.PasswordRepository using the generated user_passwords repository.
type PasswordSatisfier struct {
	repo passwordRepo
}

func NewPasswordSatisfier(repo passwordRepo) *PasswordSatisfier {
	return &PasswordSatisfier{repo: repo}
}

func (s *PasswordSatisfier) GetByUserID(ctx context.Context, userID string) (authentication.Password, error) {
	p, err := s.repo.Get(ctx, userID)
	if err != nil {
		return authentication.Password{}, err
	}
	return authentication.Password{
		UserID:   p.UserID,
		Hash:     p.PasswordHash,
		Verified: p.PasswordVerified,
	}, nil
}

func (s *PasswordSatisfier) Create(ctx context.Context, userID, hash string) error {
	_, err := s.repo.Create(ctx, userpasswords.CreateUserPassword{
		UserID:            userID,
		PasswordHash:      hash,
		PasswordChangedAt: time.Now().UTC(),
	})
	return err
}

func (s *PasswordSatisfier) Update(ctx context.Context, userID, hash string) error {
	now := time.Now().UTC()
	_, err := s.repo.Update(ctx, userID, userpasswords.UpdateUserPassword{
		PasswordHash:      &hash,
		PasswordChangedAt: &now,
	})
	return err
}

func (s *PasswordSatisfier) DeleteByUserID(ctx context.Context, userID string) error {
	return s.repo.Delete(ctx, userID)
}

func (s *PasswordSatisfier) SetVerified(ctx context.Context, userID string) error {
	return s.repo.SetVerified(ctx, time.Now().UTC(), userID)
}
`,
	"core/auth/authentication/satisfiers/security_events.go": `package satisfiers

import (
	"context"
	"encoding/json"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/securityevents"
)

var _ authentication.SecurityEventRepository = (*SecurityEventSatisfier)(nil)

type securityEventRepo interface {
	Create(ctx context.Context, input securityevents.CreateSecurityEvent) (securityevents.SecurityEvent, error)
}

// SecurityEventSatisfier satisfies authentication.SecurityEventRepository
// using the generated security_events repository.
type SecurityEventSatisfier struct {
	repo securityEventRepo
}

func NewSecurityEventSatisfier(repo securityEventRepo) *SecurityEventSatisfier {
	return &SecurityEventSatisfier{repo: repo}
}

func (s *SecurityEventSatisfier) Create(ctx context.Context, event authentication.SecurityEvent) error {
	var details *json.RawMessage
	if event.Details != nil {
		data, err := json.Marshal(event.Details)
		if err != nil {
			return err
		}
		raw := json.RawMessage(data)
		details = &raw
	}
	_, err := s.repo.Create(ctx, securityevents.CreateSecurityEvent{
		UserID:       strPtr(event.UserID),
		EventType:    event.EventType,
		EventStatus:  event.EventStatus,
		EventDetails: details,
		IpAddress:    strPtr(event.IPAddress),
		UserAgent:    strPtr(event.UserAgent),
	})
	return err
}
`,
	"core/auth/authentication/satisfiers/service_account_principals.go": `package satisfiers

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/serviceaccounts"
)

var _ authentication.ServiceAccountPrincipalRepository = (*ServiceAccountPrincipalSatisfier)(nil)

type serviceAccountPrincipalRepo interface {
	GetPrincipalInfo(ctx context.Context, serviceAccountID string) (serviceaccounts.GetPrincipalInfoResult, error)
}

// ServiceAccountPrincipalSatisfier satisfies authentication.ServiceAccountPrincipalRepository
// using the generated service_accounts repository.
type ServiceAccountPrincipalSatisfier struct {
	repo serviceAccountPrincipalRepo
}

func NewServiceAccountPrincipalSatisfier(repo serviceAccountPrincipalRepo) *ServiceAccountPrincipalSatisfier {
	return &ServiceAccountPrincipalSatisfier{repo: repo}
}

func (s *ServiceAccountPrincipalSatisfier) GetPrincipalInfo(ctx context.Context, serviceAccountID string) (authentication.ServiceAccountPrincipal, error) {
	info, err := s.repo.GetPrincipalInfo(ctx, serviceAccountID)
	if err != nil {
		return authentication.ServiceAccountPrincipal{}, err
	}

	return authentication.ServiceAccountPrincipal{
		ActAsUser:   info.ActAsUser,
		OwnerUserID: info.OwnerUserID,
	}, nil
}
`,
	"core/auth/authentication/satisfiers/sessions.go": `package satisfiers

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/sessions"
)

var _ authentication.SessionRepository = (*SessionSatisfier)(nil)

type sessionRepo interface {
	Create(ctx context.Context, input sessions.CreateSession) (sessions.Session, error)
	Update(ctx context.Context, sessionID string, parentUserID string, input sessions.UpdateSession) (sessions.Session, error)
	GetByTokenHash(ctx context.Context, sessionTokenHash string) (sessions.Session, error)
	GetByRefreshHash(ctx context.Context, refreshTokenHash string) (sessions.Session, error)
	GetByPreviousRefreshHash(ctx context.Context, previousRefreshTokenHash string) (sessions.Session, error)
	Delete(ctx context.Context, sessionID string, parentUserID string) error
	DeleteAllForUser(ctx context.Context, parentUserID string) error
	DeleteAllForUserExcept(ctx context.Context, parentUserID string, sessionID string) error
}

// SessionSatisfier satisfies authentication.SessionRepository using the generated sessions repository.
type SessionSatisfier struct {
	repo sessionRepo
}

func NewSessionSatisfier(repo sessionRepo) *SessionSatisfier {
	return &SessionSatisfier{repo: repo}
}

func (s *SessionSatisfier) Create(ctx context.Context, sess authentication.Session) (authentication.Session, error) {
	created, err := s.repo.Create(ctx, sessions.CreateSession{
		SessionID:                sess.SessionID,
		ParentUserID:             sess.UserID,
		SessionTokenHash:         sess.TokenHash,
		RefreshTokenHash:         &sess.RefreshTokenHash,
		PreviousRefreshTokenHash: &sess.PreviousRefreshHash,
		RotationCount:            &sess.RotationCount,
		ExpiresAt:                sess.ExpiresAt,
	})
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(created), nil
}

func (s *SessionSatisfier) GetByTokenHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByTokenHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) GetByRefreshHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByRefreshHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) GetByPreviousRefreshHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByPreviousRefreshHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) Update(ctx context.Context, sess authentication.Session) error {
	_, err := s.repo.Update(ctx, sess.SessionID, sess.UserID, sessions.UpdateSession{
		SessionTokenHash:         &sess.TokenHash,
		RefreshTokenHash:         &sess.RefreshTokenHash,
		PreviousRefreshTokenHash: &sess.PreviousRefreshHash,
		RotationCount:            &sess.RotationCount,
		ExpiresAt:                &sess.ExpiresAt,
	})
	return err
}

func (s *SessionSatisfier) Delete(ctx context.Context, userID, sessionID string) error {
	return s.repo.Delete(ctx, sessionID, userID)
}

func (s *SessionSatisfier) DeleteAllForUser(ctx context.Context, userID string) error {
	return s.repo.DeleteAllForUser(ctx, userID) // userID maps to parent_user_id
}

func (s *SessionSatisfier) DeleteAllForUserExcept(ctx context.Context, userID, exceptSessionID string) error {
	return s.repo.DeleteAllForUserExcept(ctx, userID, exceptSessionID) // userID maps to parent_user_id
}

func toAuthSession(sess sessions.Session) authentication.Session {
	as := authentication.Session{
		SessionID: sess.SessionID,
		UserID:    sess.ParentUserID,
		TokenHash: sess.SessionTokenHash,
		ExpiresAt: sess.ExpiresAt,
	}
	if sess.RefreshTokenHash != nil {
		as.RefreshTokenHash = *sess.RefreshTokenHash
	}
	if sess.PreviousRefreshTokenHash != nil {
		as.PreviousRefreshHash = *sess.PreviousRefreshTokenHash
	}
	if sess.RotationCount != nil {
		as.RotationCount = *sess.RotationCount
	}
	return as
}
`,
	"core/auth/authentication/satisfiers/users.go": `package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
)

var _ authentication.UserRepository = (*UserSatisfier)(nil)

type userRepo interface {
	Get(ctx context.Context, userID string) (users.User, error)
	GetByEmail(ctx context.Context, email string) (users.User, error)
	Create(ctx context.Context, input users.CreateUser) (users.User, error)
	SetEmailVerified(ctx context.Context, updatedAt time.Time, userID string) error
	SetLastLogin(ctx context.Context, lastLoginAt time.Time, updatedAt time.Time, userID string) error
}

// UserSatisfier satisfies authentication.UserRepository using the generated users repository.
type UserSatisfier struct {
	repo userRepo
}

func NewUserSatisfier(repo userRepo) *UserSatisfier {
	return &UserSatisfier{repo: repo}
}

func (s *UserSatisfier) Get(ctx context.Context, id string) (authentication.User, error) {
	u, err := s.repo.Get(ctx, id)
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) GetByEmail(ctx context.Context, email string) (authentication.User, error) {
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) Create(ctx context.Context, input authentication.CreateUserInput) (authentication.User, error) {
	u, err := s.repo.Create(ctx, users.CreateUser{
		Email:         input.Email,
		DisplayName:   &input.DisplayName,
		EmailVerified: input.EmailVerified,
	})
	if err != nil {
		return authentication.User{}, err
	}
	return toAuthUser(u), nil
}

func (s *UserSatisfier) SetEmailVerified(ctx context.Context, id string) error {
	return s.repo.SetEmailVerified(ctx, time.Now().UTC(), id)
}

func (s *UserSatisfier) SetLastLogin(ctx context.Context, id string, at time.Time) error {
	return s.repo.SetLastLogin(ctx, at, time.Now().UTC(), id)
}

func toAuthUser(u users.User) authentication.User {
	dn := ""
	if u.DisplayName != nil {
		dn = *u.DisplayName
	}
	return authentication.User{
		UserID:        u.UserID,
		Email:         u.Email,
		DisplayName:   dn,
		EmailVerified: u.EmailVerified,
		Active:        u.RecordState == "active",
	}
}
`,
	"core/auth/authentication/satisfiers/verification_codes.go": `package satisfiers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/verificationcodes"
)

var _ authentication.VerificationCodeRepository = (*VerificationCodeSatisfier)(nil)

type verificationCodeRepo interface {
	Create(ctx context.Context, input verificationcodes.CreateVerificationCode) (verificationcodes.VerificationCode, error)
	GetByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string, now time.Time) (verificationcodes.VerificationCode, error)
	DeleteByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string) error
	IncrementAttempts(ctx context.Context, identifier string, purpose string) (verificationcodes.IncrementAttemptsResult, error)
}

// VerificationCodeSatisfier satisfies authentication.VerificationCodeRepository
// using the generated verification_codes repository.
type VerificationCodeSatisfier struct {
	repo verificationCodeRepo
}

func NewVerificationCodeSatisfier(repo verificationCodeRepo) *VerificationCodeSatisfier {
	return &VerificationCodeSatisfier{repo: repo}
}

func (s *VerificationCodeSatisfier) Create(ctx context.Context, code authentication.VerificationCode) error {
	var data *json.RawMessage
	if code.Data != nil {
		raw := json.RawMessage(code.Data)
		data = &raw
	}
	_, err := s.repo.Create(ctx, verificationcodes.CreateVerificationCode{
		Identifier:   code.Identifier,
		CodeHash:     code.CodeHash,
		Purpose:      code.Purpose,
		Data:         data,
		AttemptCount: code.AttemptCount,
		ExpiresAt:    code.ExpiresAt,
	})
	return err
}

func (s *VerificationCodeSatisfier) Get(ctx context.Context, identifier, purpose string) (authentication.VerificationCode, error) {
	vc, err := s.repo.GetByIdentifierAndPurpose(ctx, identifier, purpose, time.Now().UTC())
	if err != nil {
		return authentication.VerificationCode{}, err
	}
	c := authentication.VerificationCode{
		Identifier:   vc.Identifier,
		CodeHash:     vc.CodeHash,
		Purpose:      vc.Purpose,
		ExpiresAt:    vc.ExpiresAt,
		AttemptCount: vc.AttemptCount,
	}
	if vc.Data != nil {
		c.Data = []byte(*vc.Data)
	}
	return c, nil
}

func (s *VerificationCodeSatisfier) Delete(ctx context.Context, identifier, purpose string) error {
	return s.repo.DeleteByIdentifierAndPurpose(ctx, identifier, purpose)
}

func (s *VerificationCodeSatisfier) IncrementAttempts(ctx context.Context, identifier, purpose string) (int, error) {
	result, err := s.repo.IncrementAttempts(ctx, identifier, purpose)
	if err != nil {
		return 0, err
	}
	return result.AttemptCount, nil
}
`,
	"core/auth/authentication/satisfiers/verification_tokens.go": `package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/verificationtokens"
)

var _ authentication.VerificationTokenRepository = (*VerificationTokenSatisfier)(nil)

type verificationTokenRepo interface {
	Create(ctx context.Context, input verificationtokens.CreateVerificationToken) (verificationtokens.VerificationToken, error)
	GetByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string, now time.Time) (verificationtokens.VerificationToken, error)
	DeleteByIdentifierAndPurpose(ctx context.Context, identifier string, purpose string) error
	DeleteByUserIDAndPurpose(ctx context.Context, userID string, purpose string) error
}

// VerificationTokenSatisfier satisfies authentication.VerificationTokenRepository
// using the generated verification_tokens repository.
type VerificationTokenSatisfier struct {
	repo verificationTokenRepo
}

func NewVerificationTokenSatisfier(repo verificationTokenRepo) *VerificationTokenSatisfier {
	return &VerificationTokenSatisfier{repo: repo}
}

func (s *VerificationTokenSatisfier) Create(ctx context.Context, token authentication.VerificationToken) error {
	_, err := s.repo.Create(ctx, verificationtokens.CreateVerificationToken{
		TokenHash:  token.TokenHash,
		Purpose:    token.Purpose,
		Identifier: token.Identifier,
		UserID:     &token.UserID,
		ExpiresAt:  token.ExpiresAt,
	})
	return err
}

func (s *VerificationTokenSatisfier) Get(ctx context.Context, identifier, purpose string) (authentication.VerificationToken, error) {
	vt, err := s.repo.GetByIdentifierAndPurpose(ctx, identifier, purpose, time.Now().UTC())
	if err != nil {
		return authentication.VerificationToken{}, err
	}
	t := authentication.VerificationToken{
		Identifier: vt.Identifier,
		TokenHash:  vt.TokenHash,
		Purpose:    vt.Purpose,
		ExpiresAt:  vt.ExpiresAt,
	}
	if vt.UserID != nil {
		t.UserID = *vt.UserID
	}
	return t, nil
}

func (s *VerificationTokenSatisfier) Delete(ctx context.Context, identifier, purpose string) error {
	return s.repo.DeleteByIdentifierAndPurpose(ctx, identifier, purpose)
}

func (s *VerificationTokenSatisfier) DeleteByUserIDAndPurpose(ctx context.Context, userID, purpose string) error {
	return s.repo.DeleteByUserIDAndPurpose(ctx, userID, purpose)
}
`,
	"core/auth/authorization/satisfiers/authorization_store.go": `package satisfiers

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/core/repositories/rebac/rebacrelationships"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

type relationshipRepo interface {
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]rebacrelationships.RebacRelationship, error)
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error)
	BulkCreate(ctx context.Context, inputs []rebacrelationships.CreateRebacRelationship) ([]rebacrelationships.RebacRelationship, error)
	DeleteAllForResource(ctx context.Context, resourceType, resourceID string) error
	DeleteByTuple(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error
	DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)
	ListBySubject(ctx context.Context, filter rebacrelationships.FilterListBySubject, subjectType, subjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]rebacrelationships.RebacRelationship, fop.Pagination, error)
	ListByResource(ctx context.Context, filter rebacrelationships.FilterListByResource, resourceType, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]rebacrelationships.RebacRelationship, fop.Pagination, error)
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error)
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error)
	LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error)
}

// AuthorizationStoreSatisfier satisfies authorization.Storer using the generated
// rebac_relationships repository.
type AuthorizationStoreSatisfier struct {
	repo       relationshipRepo
	generateID func() (string, error)
}

// Option configures an AuthorizationStoreSatisfier.
type Option func(*AuthorizationStoreSatisfier)

// WithGenerateID overrides the default relationship ID generator
// (cryptids.GenerateID) — inject a deterministic generator in tests or an
// alternative scheme without touching the adapter.
func WithGenerateID(fn func() (string, error)) Option {
	return func(s *AuthorizationStoreSatisfier) { s.generateID = fn }
}

func NewAuthorizationStoreSatisfier(repo relationshipRepo, opts ...Option) *AuthorizationStoreSatisfier {
	s := &AuthorizationStoreSatisfier{
		repo:       repo,
		generateID: cryptids.GenerateID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

var _ authorization.Storer = (*AuthorizationStoreSatisfier)(nil)

// =============================================================================
// Permission Checks
// =============================================================================

func (s *AuthorizationStoreSatisfier) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.repo.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]authorization.RelationTarget, error) {
	rels, err := s.repo.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}

	targets := make([]authorization.RelationTarget, len(rels))
	for i, r := range rels {
		targets[i] = authorization.RelationTarget{
			SubjectType:     r.SubjectType,
			SubjectID:       r.SubjectID,
			SubjectRelation: r.SubjectRelation,
		}
	}
	return targets, nil
}

func (s *AuthorizationStoreSatisfier) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.repo.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	return s.repo.CheckBatchDirect(ctx, resourceType, resourceIDs, relation, subjectType, subjectID)
}

// =============================================================================
// Relationship CRUD
// =============================================================================

func (s *AuthorizationStoreSatisfier) CreateRelationships(ctx context.Context, relationships []authorization.CreateRelationship) error {
	if len(relationships) == 0 {
		return nil
	}

	inputs := make([]rebacrelationships.CreateRebacRelationship, len(relationships))
	for i, r := range relationships {
		id, err := s.generateID()
		if err != nil {
			return fmt.Errorf("generate relationship id: %w", err)
		}
		inputs[i] = rebacrelationships.CreateRebacRelationship{
			RelationshipID:  id,
			ResourceType:    r.ResourceType,
			ResourceID:      r.ResourceID,
			Relation:        r.Relation,
			SubjectType:     r.SubjectType,
			SubjectID:       r.SubjectID,
			SubjectRelation: r.SubjectRelation,
		}
	}

	_, err := s.repo.BulkCreate(ctx, inputs)
	return err
}

func (s *AuthorizationStoreSatisfier) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return s.repo.DeleteAllForResource(ctx, resourceType, resourceID)
}

func (s *AuthorizationStoreSatisfier) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return s.repo.DeleteByTuple(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return s.repo.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// =============================================================================
// Counts
// =============================================================================

func (s *AuthorizationStoreSatisfier) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return s.repo.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
}

// =============================================================================
// Listing
// =============================================================================

func (s *AuthorizationStoreSatisfier) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter authorization.SubjectRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]authorization.SubjectRelationship, fop.Pagination, error) {
	repoFilter := rebacrelationships.FilterListBySubject{
		ResourceType: filter.ResourceType,
		Relation:     filter.Relation,
	}

	rels, pagination, err := s.repo.ListBySubject(ctx, repoFilter, subjectType, subjectID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}

	result := make([]authorization.SubjectRelationship, len(rels))
	for i, r := range rels {
		result[i] = authorization.SubjectRelationship{
			ResourceType: r.ResourceType,
			ResourceID:   r.ResourceID,
			Relation:     r.Relation,
			CreatedAt:    r.CreatedAt,
		}
	}

	return result, pagination, nil
}

func (s *AuthorizationStoreSatisfier) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter authorization.ResourceRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]authorization.ResourceRelationship, fop.Pagination, error) {
	repoFilter := rebacrelationships.FilterListByResource{
		SubjectType: filter.SubjectType,
		Relation:    filter.Relation,
	}

	rels, pagination, err := s.repo.ListByResource(ctx, repoFilter, resourceType, resourceID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}

	result := make([]authorization.ResourceRelationship, len(rels))
	for i, r := range rels {
		result[i] = authorization.ResourceRelationship{
			SubjectType: r.SubjectType,
			SubjectID:   r.SubjectID,
			Relation:    r.Relation,
			CreatedAt:   r.CreatedAt,
		}
	}

	return result, pagination, nil
}

// =============================================================================
// LookupResources
// =============================================================================

func (s *AuthorizationStoreSatisfier) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	return s.repo.LookupResourceIDs(ctx, resourceType, relations, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	return s.repo.LookupResourceIDsByRelationTarget(ctx, resourceType, relation, targetType, targetIDs)
}

func (s *AuthorizationStoreSatisfier) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	return s.repo.LookupDescendantResourceIDs(ctx, resourceType, relation, subjectType, rootIDs)
}
`,
	"core/auth/invitations/satisfiers/invitations.go": `// Package satisfiers adapts generated repositories to the interfaces the
// invitations engine consumes. Each satisfier wraps a generated repository
// and converts between engine-owned types and generated repository types.
package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/invitations"
	invitationsrepo "github.com/gopernicus/gopernicus/core/repositories/rebac/invitations"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

var _ invitations.InvitationRepository = (*InvitationSatisfier)(nil)

type invitationRepo interface {
	Create(ctx context.Context, input invitationsrepo.CreateInvitation) (invitationsrepo.Invitation, error)
	Get(ctx context.Context, invitationID string) (invitationsrepo.Invitation, error)
	GetByToken(ctx context.Context, tokenHash string, now time.Time) (invitationsrepo.Invitation, error)
	Update(ctx context.Context, invitationID string, input invitationsrepo.UpdateInvitation) (invitationsrepo.Invitation, error)
	Delete(ctx context.Context, invitationID string) error
	ListByResource(ctx context.Context, filter invitationsrepo.FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
	ListBySubject(ctx context.Context, filter invitationsrepo.FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
	ListByIdentifier(ctx context.Context, filter invitationsrepo.FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
}

// InvitationSatisfier satisfies invitations.InvitationRepository using the
// generated invitations repository. Engine types are field-identical to the
// generated types, so all mappings are plain struct conversions — the
// compiler flags any drift between the two.
type InvitationSatisfier struct {
	repo invitationRepo
}

func NewInvitationSatisfier(repo invitationRepo) *InvitationSatisfier {
	return &InvitationSatisfier{repo: repo}
}

func (s *InvitationSatisfier) Create(ctx context.Context, input invitations.CreateInvitation) (invitations.Invitation, error) {
	created, err := s.repo.Create(ctx, invitationsrepo.CreateInvitation(input))
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(created), nil
}

func (s *InvitationSatisfier) Get(ctx context.Context, invitationID string) (invitations.Invitation, error) {
	inv, err := s.repo.Get(ctx, invitationID)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(inv), nil
}

func (s *InvitationSatisfier) GetByToken(ctx context.Context, tokenHash string, now time.Time) (invitations.Invitation, error) {
	inv, err := s.repo.GetByToken(ctx, tokenHash, now)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(inv), nil
}

func (s *InvitationSatisfier) Update(ctx context.Context, invitationID string, input invitations.UpdateInvitation) (invitations.Invitation, error) {
	updated, err := s.repo.Update(ctx, invitationID, invitationsrepo.UpdateInvitation(input))
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(updated), nil
}

func (s *InvitationSatisfier) Delete(ctx context.Context, invitationID string) error {
	return s.repo.Delete(ctx, invitationID)
}

func (s *InvitationSatisfier) ListByResource(ctx context.Context, filter invitations.FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListByResource(ctx, invitationsrepo.FilterListByResource(filter), resourceType, resourceID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func (s *InvitationSatisfier) ListBySubject(ctx context.Context, filter invitations.FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListBySubject(ctx, invitationsrepo.FilterListBySubject(filter), resolvedSubjectID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func (s *InvitationSatisfier) ListByIdentifier(ctx context.Context, filter invitations.FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListByIdentifier(ctx, invitationsrepo.FilterListByIdentifier(filter), identifier, identifierType, now, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func toEngineInvitations(records []invitationsrepo.Invitation) []invitations.Invitation {
	out := make([]invitations.Invitation, len(records))
	for i, record := range records {
		out[i] = invitations.Invitation(record)
	}
	return out
}
`,
}
