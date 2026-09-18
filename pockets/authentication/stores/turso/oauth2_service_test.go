package turso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type protocolTestSigner struct {
	now  time.Time
	fail bool
}

func (s *protocolTestSigner) Sign(claims map[string]any, expiry time.Time) (string, error) {
	if s.fail {
		return "", errors.New("injected signer failure")
	}
	claims = maps.Clone(claims)
	claims["exp"], claims["iat"] = expiry.Unix(), s.now.Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("test signing key, never production"))
	mac.Write([]byte(raw))
	return raw + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *protocolTestSigner) Verify(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, sdk.ErrUnauthorized
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, sdk.ErrUnauthorized
	}
	mac := hmac.New(sha256.New, []byte("test signing key, never production"))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, sdk.ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, sdk.ErrUnauthorized
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, sdk.ErrUnauthorized
	}
	exp, ok := claims["exp"].(float64)
	if !ok || !s.now.Before(time.Unix(int64(exp), 0)) {
		return nil, sdk.ErrUnauthorized
	}
	return claims, nil
}

func (s *protocolTestSigner) VerifyOAuth2AccessToken(_ context.Context, raw string) (oauth2.AccessToken, error) {
	claims, err := s.Verify(raw)
	if err != nil {
		return oauth2.AccessToken{}, err
	}
	if claims["session_profile"] != string(session.ProfileDelegated) {
		return oauth2.AccessToken{}, sdk.ErrUnauthorized
	}
	aud, ok := claims["aud"].([]any)
	if !ok || len(aud) != 1 {
		return oauth2.AccessToken{}, sdk.ErrUnauthorized
	}
	read := func(key string) string { value, _ := claims[key].(string); return value }
	audience, _ := aud[0].(string)
	actor := ""
	if act, ok := claims["act"].(map[string]any); ok {
		actor, _ = act["sub"].(string)
	}
	exp := claims["exp"].(float64)
	return oauth2.AccessToken{UserID: read("user_id"), SessionID: read("session_id"), Issuer: read("iss"), Audience: audience,
		ClientID: read("client_id"), OriginClientID: read("origin_client_id"), ActorID: actor, ExpiresAt: time.Unix(int64(exp), 0)}, nil
}

type protocolTestClients struct{ revoked bool }

func (r *protocolTestClients) Resolve(_ context.Context, id string) (oauth2.Client, error) {
	if r.revoked || (id != "https://client.example/metadata" && id != "https://other.example/metadata") {
		return oauth2.Client{}, oauth2.ErrInvalidClient
	}
	return oauth2.Client{ID: id, Name: "Trusted client", RedirectURIs: []string{"https://client.example/callback"}}, nil
}

func TestOAuthProtocolWithSQLiteIndependentSessions(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	now := time.Now().UTC().Truncate(time.Second)
	web := seedOAuthBrowser(t, db, "owner", now)
	signer := &protocolTestSigner{now: now}
	resolver := &protocolTestClients{}
	cfg := oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example", ConfidentialClientID: "mcp",
		ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("confidential test secret"))}}
	svc, err := oauth2.New(oauth2.Repositories{OAuth2: repos.OAuth2, Users: repos.Users, Sessions: repos.Sessions, SecurityEvents: repos.SecurityEvents}, signer, resolver, signer,
		environment.ModeProduction, cfg, oauth2.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	req := oauth2.AuthorizationRequest{ResponseType: "code", ClientID: "https://client.example/metadata", RedirectURI: "https://client.example/callback", Resource: cfg.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256", State: "client-private-state"}
	prepare := func() oauth2.AuthorizationResult {
		t.Helper()
		consent, err := svc.PrepareAuthorization(t.Context(), req, web.UserID, web.ID)
		if err != nil {
			t.Fatal(err)
		}
		if consent.Client.ID != req.ClientID || consent.RedirectURI != req.RedirectURI || consent.Resource != cfg.MCPResource {
			t.Fatalf("consent summary: %+v", consent)
		}
		if _, err := svc.Approve(t.Context(), consent.ConsentToken, "foreign-user", web.ID); !errors.Is(err, oauth2.ErrAccessDenied) {
			t.Fatalf("consent user rebinding: %v", err)
		}
		if _, err := svc.Approve(t.Context(), consent.ConsentToken, web.UserID, "foreign-session"); !errors.Is(err, oauth2.ErrAccessDenied) {
			t.Fatalf("consent session rebinding: %v", err)
		}
		denied, err := svc.Deny(t.Context(), consent.ConsentToken, web.UserID, web.ID)
		if err != nil || denied.Error != "access_denied" || denied.State != req.State || denied.RedirectURI != req.RedirectURI {
			t.Fatalf("deny: %+v %v", denied, err)
		}
		approved, err := svc.Approve(t.Context(), consent.ConsentToken, web.UserID, web.ID)
		if err != nil || approved.State != req.State || approved.Code == "" {
			t.Fatalf("approve: %+v %v", approved, err)
		}
		return approved
	}
	aCode, bCode := prepare(), prepare()
	if err := repos.SessionManagement.DeleteForUser(t.Context(), web.ID, web.UserID); err != nil {
		t.Fatal(err)
	}
	codeReq := func(code string) oauth2.CodeRequest {
		return oauth2.CodeRequest{Code: code, ClientID: req.ClientID, RedirectURI: req.RedirectURI, Resource: req.Resource, CodeVerifier: verifier}
	}
	signer.fail = true
	if _, err := svc.RedeemCode(t.Context(), codeReq(aCode.Code)); err == nil {
		t.Fatal("signer failure ignored")
	}
	signer.fail = false
	badPKCE := codeReq(aCode.Code)
	badPKCE.CodeVerifier = strings.Repeat("x", 43)
	if _, err := svc.RedeemCode(t.Context(), badPKCE); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("invalid verifier: %v", err)
	}
	a, err := svc.RedeemCode(t.Context(), codeReq(aCode.Code))
	if err != nil {
		t.Fatalf("signing error consumed code or web logout blocked grant: %v", err)
	}
	b, err := svc.RedeemCode(t.Context(), codeReq(bCode.Code))
	if err != nil {
		t.Fatal(err)
	}
	if !session.IsDelegatedRefreshToken(a.RefreshToken) || a.RefreshToken == b.RefreshToken || a.TokenType != "Bearer" {
		t.Fatalf("delegated refresh credentials not independent: %+v %+v", a, b)
	}
	if _, err := svc.RedeemCode(t.Context(), codeReq(aCode.Code)); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("second redemption: %v", err)
	}
	aToken, err := signer.VerifyOAuth2AccessToken(t.Context(), a.AccessToken)
	if err != nil || aToken.Audience != cfg.MCPResource || aToken.ClientID != req.ClientID || aToken.OriginClientID != "" || aToken.ActorID != "" {
		t.Fatalf("direct token profile: %+v %v", aToken, err)
	}
	bToken, err := signer.VerifyOAuth2AccessToken(t.Context(), b.AccessToken)
	if err != nil || aToken.SessionID == bToken.SessionID {
		t.Fatalf("connection session identity: %+v %v", bToken, err)
	}
	web2, _ := session.NewSession(web.UserID, time.Hour, now)
	web2.RefreshTokenHash = "second-web-session"
	if _, err := repos.Sessions.Create(t.Context(), web2); err != nil {
		t.Fatal(err)
	}
	proof, err := svc.AuthenticateClient("mcp", "confidential test secret")
	if err != nil {
		t.Fatal(err)
	}
	active, err := svc.Introspect(t.Context(), proof, a.AccessToken)
	if err != nil || !active.Active || active.SessionID != aToken.SessionID || active.Subject != "user:"+web.UserID {
		t.Fatalf("introspect: %+v %v", active, err)
	}
	exchange := oauth2.ExchangeRequest{SubjectToken: a.AccessToken, SubjectTokenType: oauth2.AccessTokenType, RequestedTokenType: oauth2.AccessTokenType, Resource: cfg.APIResource}
	api, err := svc.Exchange(t.Context(), proof, exchange)
	if err != nil || api.RefreshToken != "" || api.IssuedTokenType != oauth2.AccessTokenType {
		t.Fatalf("exchange: %+v %v", api, err)
	}
	apiToken, err := signer.VerifyOAuth2AccessToken(t.Context(), api.AccessToken)
	if err != nil || apiToken.Audience != cfg.APIResource || apiToken.SessionID != aToken.SessionID || apiToken.ClientID != "mcp" || apiToken.ActorID != "mcp" || apiToken.OriginClientID != req.ClientID {
		t.Fatalf("exchange attribution: %+v %v", apiToken, err)
	}
	if answer, err := svc.Introspect(t.Context(), proof, api.AccessToken); err != nil || answer.Active {
		t.Fatalf("MCP introspected API-only token: %+v %v", answer, err)
	}
	exchange.SubjectToken = api.AccessToken
	if _, err := svc.Exchange(t.Context(), proof, exchange); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("chained exchange accepted: %v", err)
	}
	if err := svc.Revoke(t.Context(), oauth2.RevokeRequest{Token: a.AccessToken, ClientID: "https://other.example/metadata"}); err != nil {
		t.Fatal(err)
	}
	requireOAuthLive(t, repos, aToken.SessionID, bToken.SessionID, web2.ID)
	refresh := oauth2.RefreshRequest{RefreshToken: a.RefreshToken, ClientID: req.ClientID, Resource: req.Resource}
	signer.fail = true
	if _, err := svc.Refresh(t.Context(), refresh); err == nil {
		t.Fatal("refresh signing failure ignored")
	}
	signer.fail = false
	resolver.revoked = true
	if _, err := svc.Refresh(t.Context(), refresh); !errors.Is(err, oauth2.ErrInvalidClient) {
		t.Fatalf("withdrawn client refreshed: %v", err)
	}
	if answer, err := svc.Introspect(t.Context(), proof, a.AccessToken); err != nil || answer.Active {
		t.Fatalf("withdrawn client remains active: %+v %v", answer, err)
	}
	resolver.revoked = false
	rotated, err := svc.Refresh(t.Context(), refresh)
	if err != nil || rotated.RefreshToken == a.RefreshToken {
		t.Fatalf("signer failure consumed refresh: %+v %v", rotated, err)
	}
	if _, err := svc.Refresh(t.Context(), refresh); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("spent refresh not detected: %v", err)
	}
	if answer, err := svc.Introspect(t.Context(), proof, rotated.AccessToken); err != nil || answer.Active {
		t.Fatalf("refresh reuse failed to revoke: %+v %v", answer, err)
	}
	if _, err := svc.Exchange(t.Context(), proof, oauth2.ExchangeRequest{SubjectToken: a.AccessToken, SubjectTokenType: oauth2.AccessTokenType, Resource: cfg.APIResource}); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("revoked grant exchanged: %v", err)
	}
	requireOAuthLive(t, repos, bToken.SessionID, web2.ID)
	if err := svc.Revoke(t.Context(), oauth2.RevokeRequest{Token: b.RefreshToken, ClientID: req.ClientID}); err != nil {
		t.Fatal(err)
	}
	requireOAuthLive(t, repos, web2.ID)
	if answer, err := svc.Introspect(t.Context(), proof, b.AccessToken); err != nil || answer.Active {
		t.Fatalf("revocation introspection: %+v %v", answer, err)
	}
	events, err := repos.SecurityEvents.List(t.Context(), securityevent.ListFilter{}, list.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, event := range events.Items {
		kinds[event.EventType] = true
	}
	for _, kind := range []string{oauth2.TypeConsentApproved, oauth2.TypeConnectionCreated, oauth2.TypeRefresh, oauth2.TypeRefreshReuse, oauth2.TypeTokenExchanged, oauth2.TypeRevocationRequested} {
		if !kinds[kind] {
			t.Errorf("missing audit event %s", kind)
		}
	}
	auditJSON, err := json.Marshal(events.Items)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{aCode.Code, a.AccessToken, a.RefreshToken, b.AccessToken, b.RefreshToken, api.AccessToken, rotated.RefreshToken, verifier, req.State, "confidential test secret"} {
		if strings.Contains(string(auditJSON), secret) {
			t.Error("credential material leaked into audit")
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Introspect(t.Context(), proof, a.AccessToken); err == nil {
		t.Fatal("database outage hidden as inactive token")
	}
}
