package authenticationhttp

import (
	"context"
	"log/slog"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// authenticationFixture keeps mutable inputs for existing failure matrices.
// Public constructor calls below map each field to its explicit dependency or option.
type authenticationFixture struct {
	RuntimeMode          environment.Mode
	AuthenticationLimits authlogic.AuthenticationLimits
	Users                user.UserRepository
	Identifiers          identifier.IdentifierRepository
	Normalizer           identifier.Normalizer
	Passwords            user.PasswordRepository
	Sessions             session.SessionRepository
	UserAdmin            user.AdminRepository
	UserAdminCheck       authlogic.UserAdminCheck
	PasswordlessRedeem   passwordless.Repository
	ProvisionOnRedeem    bool
	ActiveSessions       session.ActiveUserRepository
	Challenges           challenge.Repository
	Protector            protection.ChallengeProtector
	PasswordResets       passwordreset.Repository
	ContactChanges       contactchange.Repository
	CredentialMutations  credential.MutationRepository
	AuthenticationGrants authgrant.Repository
	CredentialPolicy     credential.Policy
	Hasher               authlogic.Hasher
	ValidatePassword     func(context.Context, string) error
	Compromised          authlogic.CompromisedPasswordChecker
	CompromisedFailOpen  bool
	Deliver              *delivery.Router
	Queue                interface {
		Enqueue(context.Context, delivery.Command) (delivery.Receipt, error)
		Replace(context.Context, delivery.Command) (delivery.Receipt, error)
		Status(context.Context, string) (delivery.Status, error)
	}
	IdentifierKeyer       protection.IdentifierKeyer
	Limiter               ratelimiter.Limiter
	Clock                 func() time.Time
	Logger                *slog.Logger
	IDs                   sdk.IDGenerator
	PasswordFlowsDisabled bool
	RequireVerifiedEmail  bool
	SecurityEvents        securityevent.SecurityEventRepository
	Invitations           interface {
		ResolveInvitations(context.Context, string, string, string) (int, error)
	}
	OAuthAccounts           oauthaccount.OAuthAccountRepository
	OAuthStates             oauthstate.StateRepository
	Providers               []oauth.Provider
	TokenEncrypter          cryptids.Encrypter
	OAuthCallbackBase       string
	OAuthNativeRedirectURIs []string
	TrustOAuthEmail         func(string, oauth.UserInfo) bool
	RedirectAllowlist       []string
	ServiceAccounts         serviceaccount.ServiceAccountRepository
	APIKeys                 apikey.APIKeyRepository
	TokenSigner             cryptids.JWTSigner
	AccessTokenTTL          time.Duration
	RefreshTTL              time.Duration
	Passwordless            []string
	PublicAuthBaseURL       string
	PasswordResetURL        string
	OAuthLinkBaseURL        string
}
