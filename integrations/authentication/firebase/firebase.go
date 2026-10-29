// Package firebase is the Firebase Authentication preset for
// sdk/capabilities/authentication. It isolates the Firebase Admin Go SDK
// (firebase.google.com/go/v4) and the Google client companions its API forces.
//
// Two Providers share one *auth.Client: New verifies ID tokens (iss
// https://securetoken.google.com/<project>) and NewSessionCookie verifies
// server-minted session cookies (iss https://session.firebase.google.com/<project>),
// whose x509 keys are not a JWKS, so a generic OIDC verifier cannot check them.
// Both implement authentication.RevocationChecker from GetUser (Disabled and
// TokensValidAfterMillis) and can mint a session cookie from an ID token.
//
// Profile serves hosts that verify Firebase ID tokens through a generic OIDC
// verifier instead (header-only, no revocation facts, no session cookies).
//
// Identity is built from the verified *auth.Token's struct fields; the claims
// map is read only for claims the struct does not carry. Raw tokens, cookies
// and GetUser payloads are never logged or cached.
package firebase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	firebasesdk "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// EmulatorHostEnv is the variable the Admin SDK reads to switch a client to
// the Auth emulator, where tokens are unsigned and never verified.
const EmulatorHostEnv = "FIREBASE_AUTH_EMULATOR_HOST"

const (
	idTokenIssuerPrefix       = "https://securetoken.google.com/"
	sessionCookieIssuerPrefix = "https://session.firebase.google.com/"

	// SignInAnonymous is Firebase's firebase.sign_in_provider for anonymous users.
	SignInAnonymous = "anonymous"
)

var (
	// ErrEmulatorInProduction refuses a production Provider while the Admin SDK
	// is in emulator mode, which accepts unsigned tokens. It matches sdk.ErrInvalidInput.
	ErrEmulatorInProduction = fmt.Errorf("firebase: %s is set in production mode; emulator tokens are unsigned: %w", EmulatorHostEnv, sdk.ErrInvalidInput)

	// ErrInvalidConfig rejects a Config or ClientConfig. It matches sdk.ErrInvalidInput.
	ErrInvalidConfig = fmt.Errorf("firebase: invalid config: %w", sdk.ErrInvalidInput)

	_ sdkauth.Provider          = (*Provider)(nil)
	_ sdkauth.RevocationChecker = (*Provider)(nil)
)

// Provider is an authentication.Provider for one Firebase credential kind of
// one project. It also implements authentication.RevocationChecker.
type Provider struct {
	issuer     string
	projectID  string
	client     *auth.Client
	verify     func(ctx context.Context, raw string) (*auth.Token, error)
	getUser    func(ctx context.Context, uid string) (*auth.UserRecord, error)
	denySignIn []string
	now        func() time.Time
}

// Config configures New and NewSessionCookie.
type Config struct {
	// ProjectID is the Firebase project; it pins iss and aud. Required, and
	// must be the project the client was built for.
	ProjectID string

	// Mode is required. Production refuses construction while
	// FIREBASE_AUTH_EMULATOR_HOST is set; development logs a warning.
	Mode environment.Mode

	// DenySignIn lists firebase.sign_in_provider values refused outright. Nil
	// uses DefaultDenySignIn (anonymous users); a non-nil empty slice refuses none.
	DenySignIn []string

	// Logger receives the development emulator warning. Nil uses slog.Default().
	Logger *slog.Logger
}

// ClientConfig configures NewClient.
type ClientConfig struct {
	// ProjectID is the Firebase project. Required.
	ProjectID string

	// CredentialsJSON is a service-account key. Empty uses Application
	// Default Credentials, which also honor GOOGLE_APPLICATION_CREDENTIALS.
	// Never logged.
	CredentialsJSON sdk.Secret
}

// NewClient builds an Admin SDK auth client: ADC by default, or a
// service-account key. Hosts that already hold a *firebase.App pass app.Auth
// instead. Construction performs no request.
func NewClient(ctx context.Context, cfg ClientConfig) (*auth.Client, error) {
	if strings.TrimSpace(cfg.ProjectID) == "" {
		return nil, fmt.Errorf("%w: ProjectID is required", ErrInvalidConfig)
	}
	var opts []option.ClientOption
	if len(cfg.CredentialsJSON) > 0 {
		opts = append(opts, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(cfg.CredentialsJSON.Reveal())))
	}
	app, err := firebasesdk.NewApp(ctx, &firebasesdk.Config{ProjectID: cfg.ProjectID}, opts...)
	if err != nil {
		return nil, fmt.Errorf("firebase: new app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase: auth client: %w", err)
	}
	return client, nil
}

// New returns the ID-token Provider; Issuer() is https://securetoken.google.com/<project>.
func New(ctx context.Context, client *auth.Client, cfg Config) (*Provider, error) {
	p, err := newProvider(ctx, client, cfg, IDTokenIssuer(cfg.ProjectID))
	if err != nil {
		return nil, err
	}
	p.verify = client.VerifyIDToken
	return p, nil
}

// NewSessionCookie returns the session-cookie Provider; Issuer() is
// https://session.firebase.google.com/<project>.
func NewSessionCookie(ctx context.Context, client *auth.Client, cfg Config) (*Provider, error) {
	p, err := newProvider(ctx, client, cfg, SessionCookieIssuer(cfg.ProjectID))
	if err != nil {
		return nil, err
	}
	p.verify = client.VerifySessionCookie
	return p, nil
}

func newProvider(ctx context.Context, client *auth.Client, cfg Config, issuer string) (*Provider, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: client is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.ProjectID) == "" || strings.ContainsAny(cfg.ProjectID, "/ ") {
		return nil, fmt.Errorf("%w: ProjectID is required and must be a bare project id", ErrInvalidConfig)
	}
	if err := environment.ValidateMode(cfg.Mode); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if host := os.Getenv(EmulatorHostEnv); host != "" {
		if cfg.Mode.IsProduction() {
			return nil, ErrEmulatorInProduction
		}
		logger := cfg.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.WarnContext(ctx, "firebase: Auth emulator mode; tokens are UNSIGNED and accepted without signature verification",
			slog.String("emulator_host", host), slog.String("issuer", issuer))
	}
	deny := DefaultDenySignIn()
	if cfg.DenySignIn != nil {
		deny = slices.Clone(cfg.DenySignIn)
	}
	return &Provider{
		issuer:     issuer,
		projectID:  cfg.ProjectID,
		client:     client,
		getUser:    client.GetUser,
		denySignIn: deny,
		now:        time.Now,
	}, nil
}

// Issuer returns the exact iss this provider accepts.
func (p *Provider) Issuer() string { return p.issuer }

// Authenticate rejects oversize, malformed and foreign-issuer credentials
// before calling the Admin SDK, then maps the verified token. In emulator mode
// the Admin SDK also checks revocation on every call.
func (p *Provider) Authenticate(ctx context.Context, cred sdkauth.Credential) (sdkauth.Identity, error) {
	if cred.Raw == "" {
		return sdkauth.Identity{}, fmt.Errorf("%w: empty", sdkauth.ErrInvalidCredential)
	}
	if len(cred.Raw) > sdkauth.MaxCredentialBytes {
		return sdkauth.Identity{}, fmt.Errorf("%w: exceeds %d bytes", sdkauth.ErrInvalidCredential, sdkauth.MaxCredentialBytes)
	}
	iss, err := peekIssuer(cred.Raw)
	if err != nil {
		return sdkauth.Identity{}, err
	}
	if iss != p.issuer {
		return sdkauth.Identity{}, sdkauth.ErrIssuerMismatch
	}

	token, err := p.verify(ctx, cred.Raw)
	if err != nil {
		return sdkauth.Identity{}, classify(ctx, err, sdkauth.ErrInvalidCredential)
	}
	return identityFromToken(token, p.issuer, p.denySignIn)
}

// identityFromToken maps the Admin SDK's struct fields; the claims map is read
// only for profile claims and the second factor, which Token has no field for.
func identityFromToken(token *auth.Token, issuer string, denySignIn []string) (sdkauth.Identity, error) {
	if token == nil {
		return sdkauth.Identity{}, fmt.Errorf("%w: no token", sdkauth.ErrInvalidCredential)
	}
	if token.Issuer != issuer {
		return sdkauth.Identity{}, sdkauth.ErrIssuerMismatch
	}
	if token.Subject == "" {
		return sdkauth.Identity{}, fmt.Errorf("%w: missing subject", sdkauth.ErrInvalidCredential)
	}
	if token.Expires <= 0 {
		return sdkauth.Identity{}, fmt.Errorf("%w: missing exp", sdkauth.ErrInvalidCredential)
	}
	if slices.Contains(denySignIn, token.Firebase.SignInProvider) {
		return sdkauth.Identity{}, sdkauth.ErrSignInMethodDenied
	}

	id := sdkauth.Identity{
		Issuer:      issuer,
		Subject:     token.Subject,
		Email:       claimString(token.Claims, "email"),
		Phone:       claimString(token.Claims, "phone_number"),
		DisplayName: claimString(token.Claims, "name"),
		Tenant:      token.Firebase.Tenant,
		ExpiresAt:   time.Unix(token.Expires, 0).UTC(),
	}
	switch {
	case token.AuthTime > 0:
		id.AuthenticatedAt = time.Unix(token.AuthTime, 0).UTC()
		id.AuthTimeAsserted = true
	case token.IssuedAt > 0:
		id.AuthenticatedAt = time.Unix(token.IssuedAt, 0).UTC()
	}
	id.EmailVerified = id.Email != "" && claimBool(token.Claims, "email_verified")
	// Firebase sets phone_number only after SMS possession is proven.
	id.PhoneVerified = id.Phone != ""
	if fb, ok := token.Claims["firebase"].(map[string]any); ok {
		id.MFA = claimString(fb, "sign_in_second_factor") != ""
	}
	if token.Firebase.SignInProvider != "" {
		id.Attributes = map[string]string{AttributeSignInProvider: token.Firebase.SignInProvider}
	}
	return id, nil
}

func claimString(claims map[string]any, key string) string {
	s, _ := claims[key].(string)
	return s
}

func claimBool(claims map[string]any, key string) bool {
	b, _ := claims[key].(bool)
	return b
}

// peekIssuer reads iss from an UNVERIFIED compact JWT. It is used only to
// reject; acceptance always follows the Admin SDK.
func peekIssuer(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: malformed token", sdkauth.ErrInvalidCredential)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: malformed token", sdkauth.ErrInvalidCredential)
	}
	var body struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", fmt.Errorf("%w: malformed token", sdkauth.ErrInvalidCredential)
	}
	return body.Iss, nil
}
