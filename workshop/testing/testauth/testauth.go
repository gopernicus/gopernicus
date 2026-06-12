// Package testauth builds a minimal authentication/authorization stack for
// the generated security suite. The suite exercises only token rejection
// (anonymous and malformed requests → 401) and, for isolation tests, minting
// valid access tokens for distinct principals — none of which touch the
// auth repositories or the ReBAC store. So the authenticator wraps a real
// JWT signer over empty repositories, and the authorizer is constructed with
// an empty schema: enough to mount the real middleware and prove enforcement
// without standing up the full auth composition root.
package testauth

import (
	"time"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/golangjwt"
)

// testSecret signs tokens for the security suite only. Never a production key.
const testSecret = "gopernicus-security-suite-test-secret-not-for-production-use"

// Authenticator returns an authentication.Authenticator backed by a real JWT
// signer and empty repositories, plus the signer so callers can mint valid
// tokens. AuthenticateJWT verifies signatures without a database query, so the
// nil repositories are never dereferenced by the security probes.
func Authenticator(name string) (*authentication.Authenticator, *golangjwt.Signer) {
	signer, err := golangjwt.NewSigner(testSecret)
	if err != nil {
		panic("testauth: build signer: " + err.Error())
	}
	repos := authentication.NewRepositories(nil, nil, nil, nil, nil)
	auth := authentication.NewAuthenticator(name, repos, nil, signer, nil, authentication.Config{
		AccessTokenExpiry: time.Hour,
	})
	return auth, signer
}

// AuthenticatorWithRepositories returns an authenticator over real
// repositories, for harnesses that exercise the database-backed
// authentication modes — user sessions and API keys — where a seeded
// credential row must be looked up for real. Pair with the generated
// fixtures' column overrides and authentication.HashToken to seed rows
// the engine will match:
//
//	token := testauth.MintAccessToken(signer, user.UserID)
//	hash, _ := authentication.HashToken(token)
//	fixtures.CreateTestSession(t, ctx, db, user.UserID,
//		map[string]any{"session_token_hash": hash})
func AuthenticatorWithRepositories(name string, repos authentication.Repositories) (*authentication.Authenticator, *golangjwt.Signer) {
	signer, err := golangjwt.NewSigner(testSecret)
	if err != nil {
		panic("testauth: build signer: " + err.Error())
	}
	auth := authentication.NewAuthenticator(name, repos, nil, signer, nil, authentication.Config{
		AccessTokenExpiry: time.Hour,
	})
	return auth, signer
}

// MintAccessToken signs a valid access token carrying the given user id —
// the claim AuthenticateJWT reads. Use distinct ids to act as different
// principals in isolation tests.
func MintAccessToken(signer *golangjwt.Signer, userID string) string {
	token, err := signer.Sign(map[string]any{"user_id": userID}, time.Now().Add(time.Hour))
	if err != nil {
		panic("testauth: mint token: " + err.Error())
	}
	return token
}

// Authorizer returns an authorizer over an empty schema. Authentication is
// enforced ahead of authorization, so the security suite's rejection probes
// never reach the (nil) store; isolation tests that need real authorization
// supply their own schema/store.
func Authorizer() *authorization.Authorizer {
	return authorization.NewAuthorizer(nil, authorization.Schema{}, authorization.Config{})
}
