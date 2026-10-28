package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	bcrypt "github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery/command"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// Disposable fixture keys for the rotation proofs: oldKey is the pre-ring
// single key, newKey the ring key that replaces it.
var (
	rotationOldKey = strings.Repeat("0a", 32)
	rotationNewKey = strings.Repeat("0b", 32)
)

// rotationKeyVars are every key variable a rotation phase sets; each boot
// clears the ones its phase leaves out, so no phase inherits another's keys.
var rotationKeyVars = []string{
	"AUTH_JWT_SECRET", "AUTH_JWT_KEYS", "AUTH_JWT_ACTIVE_KEY",
	"AUTH_CHALLENGE_PEPPER", "AUTH_CHALLENGE_PEPPERS", "AUTH_CHALLENGE_ACTIVE_PEPPER",
	"AUTH_DELIVERY_ENCRYPTER_KEY", "AUTH_DELIVERY_ENCRYPTER_KEYS", "AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY",
	"AUTH_TOKEN_ENCRYPTER_KEY", "AUTH_TOKEN_ENCRYPTER_KEYS", "AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY",
}

// rotationHost is one fleet instance: the host's own key wiring over a store
// every instance shares, as replicas behind a load balancer share a database.
type rotationHost struct {
	*linkHost
	cfg authenticationConfig
}

// setRotationEnv sets env as the only key variables for the next boot.
func setRotationEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range rotationKeyVars {
		t.Setenv(name, env[name])
	}
}

// bootRotationHost boots an instance over repos with the key variables in env,
// through the same buildAuthConfig (and so buildKeyedCollaborators) run uses.
// /probe is a RequireAccessToken route. tune, when set, adjusts the config
// before New.
func bootRotationHost(t *testing.T, repos auth.Repositories, env map[string]string, tune func(*authenticationConfig)) *rotationHost {
	t.Helper()
	setRotationEnv(t, env)
	cfg, err := buildAuthConfig(quietLog(), environment.ModeDevelopment, nil)
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	sender := &recordingSender{}
	cfg.DeliveryMode = delivery.ModeInProcess
	cfg.DeliveryJobsAcknowledged = false
	cfg.DeliveryEphemeralAcknowledged = true
	cfg.Mailer = sender
	cfg.Hasher = bcrypt.New(bcrypt.WithCost(4))
	if tune != nil {
		tune(&cfg)
	}
	svc, err := auth.New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode, cfg.options()...)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", mountInProcess(t, svc))
	mux.Handle("/probe", svc.HTTP.RequireAccessToken()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	stop := runDelivery(t, svc)
	t.Cleanup(stop)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rotationHost{
		linkHost: &linkHost{t: t, srv: srv, svc: svc, sender: sender, origin: hostAllowedOrigins(t)[0]},
		cfg:      cfg,
	}
}

// issue registers email on first use and returns a fresh access token from
// POST /auth/token on h.
func (h *rotationHost) issue(email string) string {
	h.t.Helper()
	c := h.newClient()
	resp, body := c.do("POST", "/auth/register", `{"email":"`+email+`","password":"`+linkPassword+`","display_name":"Rotation"}`, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		h.t.Fatalf("register %s = %d; body=%s", email, resp.StatusCode, body)
	}
	resp, body = c.do("POST", "/auth/token", `{"email":"`+email+`","password":"`+linkPassword+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("token %s = %d; body=%s", email, resp.StatusCode, body)
	}
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &pair); err != nil || pair.AccessToken == "" {
		h.t.Fatalf("decode token response: %v", err)
	}
	return pair.AccessToken
}

// probe is the status of a RequireAccessToken route on h for token.
func (h *rotationHost) probe(token string) int {
	h.t.Helper()
	resp, _ := h.newClient().do("GET", "/probe", "", http.Header{"Authorization": {"Bearer " + token}})
	return resp.StatusCode
}

// tokenSegment decodes one base64url JWT segment (0 header, 1 claims).
func tokenSegment(t *testing.T, token string, i int) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[i])
	if err != nil {
		t.Fatalf("decode segment %d: %v", i, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal segment %d: %v", i, err)
	}
	return out
}

// kidOf is the token header's kid, "" when absent.
func kidOf(t *testing.T, token string) string {
	t.Helper()
	kid, _ := tokenSegment(t, token, 0)["kid"].(string)
	return kid
}

// TestJWTRotationThreePhases walks the runbook's JWT rotation across fleet
// instances sharing one store: before (single AUTH_JWT_SECRET), phase (a) (the
// ring beside the secret, legacy active), phase (b) (the new id active) and
// phase (c) (the secret dropped). Sessions live in the shared store, so only
// the signer differs between instances.
func TestJWTRotationThreePhases(t *testing.T) {
	repos := authmem.New().Repositories()
	single := map[string]string{"AUTH_JWT_SECRET": rotationOldKey}
	phaseA := map[string]string{"AUTH_JWT_SECRET": rotationOldKey, "AUTH_JWT_KEYS": "k2:" + rotationNewKey, "AUTH_JWT_ACTIVE_KEY": auth.LegacyKeyID}
	phaseB := map[string]string{"AUTH_JWT_SECRET": rotationOldKey, "AUTH_JWT_KEYS": "k2:" + rotationNewKey, "AUTH_JWT_ACTIVE_KEY": "k2"}
	phaseC := map[string]string{"AUTH_JWT_KEYS": "k2:" + rotationNewKey, "AUTH_JWT_ACTIVE_KEY": "k2"}

	// Verification off: a bearer pair is one /auth/token call.
	unverified := func(cfg *authenticationConfig) { cfg.RequireVerifiedEmail = false }
	before := bootRotationHost(t, repos, single, unverified)
	if _, ok := before.cfg.TokenSigner.(*golangjwt.Signer); !ok {
		t.Fatalf("AUTH_JWT_SECRET alone built %T, want the single-key *golangjwt.Signer", before.cfg.TokenSigner)
	}
	t0 := before.issue("rotation@example.com")
	if kid := kidOf(t, t0); kid != "" {
		t.Fatalf("a single-key token carries kid %q", kid)
	}

	a := bootRotationHost(t, repos, phaseA, unverified)
	ring, ok := a.cfg.TokenSigner.(*golangjwt.RingSigner)
	if !ok || ring.ActiveKeyID() != auth.LegacyKeyID || ring.LegacyKeyID() != auth.LegacyKeyID {
		t.Fatalf("phase (a) signer = %T active=%v; want a ring with legacy active", a.cfg.TokenSigner, ok)
	}
	ta := a.issue("rotation@example.com")
	if kid := kidOf(t, ta); kid != "" {
		t.Fatalf("phase (a) writes kid %q; with legacy active the ring must write the single-key shape", kid)
	}
	for name, got := range map[string]int{
		"phase (a) verifies the pre-ring token":    a.probe(t0),
		"the pre-ring instance verifies phase (a)": before.probe(ta),
	} {
		if got != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", name, got)
		}
	}

	b := bootRotationHost(t, repos, phaseB, unverified)
	t1 := b.issue("rotation@example.com")
	if kid := kidOf(t, t1); kid != "k2" {
		t.Fatalf("phase (b) token kid = %q, want k2", kid)
	}
	for name, got := range map[string]int{
		"phase (b) verifies the pre-ring token":       b.probe(t0),
		"phase (b) verifies the phase (a) token":      b.probe(ta),
		"phase (b) verifies its own kid=k2 token":     b.probe(t1),
		"a phase (a) instance verifies kid=k2 tokens": a.probe(t1),
	} {
		if got != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", name, got)
		}
	}
	if got := before.probe(t1); got != http.StatusUnauthorized {
		t.Errorf("a pre-ring instance verified a kid=k2 token (status %d); phase (b) must wait for the whole fleet on (a)", got)
	}

	c := bootRotationHost(t, repos, phaseC, unverified)
	if ring, ok := c.cfg.TokenSigner.(*golangjwt.RingSigner); !ok || ring.LegacyKeyID() != "" {
		t.Fatalf("phase (c) signer = %T; want a ring with no legacy id", c.cfg.TokenSigner)
	}
	for name, got := range map[string]int{
		"phase (c) refuses the pre-ring token":  c.probe(t0),
		"phase (c) refuses the phase (a) token": c.probe(ta),
	} {
		if got != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, got)
		}
	}
	t2 := c.issue("rotation@example.com")
	if kid := kidOf(t, t2); kid != "k2" {
		t.Fatalf("phase (c) token kid = %q, want k2", kid)
	}
	for name, got := range map[string]int{
		"phase (c) verifies the phase (b) token": c.probe(t1),
		"phase (c) verifies its own token":       c.probe(t2),
	} {
		if got != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", name, got)
		}
	}

	claims := tokenSegment(t, t1, 1)
	delete(claims, "exp")
	delete(claims, "iat")
	other, err := cryptids.ParseKeyRing("k9:"+strings.Repeat("0c", 32), "k9")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := golangjwt.NewRing(other)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := stranger.Sign(claims, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.probe(unknown); got != http.StatusUnauthorized {
		t.Errorf("a kid=k9 token outside the ring: status %d, want 401", got)
	}
}

// TestDeliveryRotationOpensPreRingEnvelopes seals a delivery command envelope
// the way a pre-ring instance does and opens it through the ring the host
// builds in each phase: phase (a) and (b) open it through the legacy id, phase
// (a)'s own envelopes still open on a pre-ring instance, and once the single
// key is dropped the old envelope is ErrUnsealedPayload (dead-lettered, no
// bytes in the error).
func TestDeliveryRotationOpensPreRingEnvelopes(t *testing.T) {
	keyedFor := func(env map[string]string) keyedCollaborators {
		t.Helper()
		setRotationEnv(t, env)
		keyed, err := buildKeyedCollaborators(environment.ModeDevelopment, quietLog())
		if err != nil {
			t.Fatalf("buildKeyedCollaborators: %v", err)
		}
		return keyed
	}
	envelope, err := command.NewOpaque("email", "verify_email", "rotation-key", "rotation@example.com")
	if err != nil {
		t.Fatal(err)
	}
	before := keyedFor(map[string]string{"AUTH_DELIVERY_ENCRYPTER_KEY": rotationOldKey})
	if _, ok := before.deliveryEncrypter.(*cryptids.AESGCM); !ok {
		t.Fatalf("AUTH_DELIVERY_ENCRYPTER_KEY alone built %T, want *cryptids.AESGCM", before.deliveryEncrypter)
	}
	old, err := command.Seal(before.deliveryEncrypter, envelope)
	if err != nil {
		t.Fatal(err)
	}

	a := keyedFor(map[string]string{"AUTH_DELIVERY_ENCRYPTER_KEY": rotationOldKey, "AUTH_DELIVERY_ENCRYPTER_KEYS": "d2:" + rotationNewKey, "AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY": auth.LegacyKeyID})
	sealedA, err := command.Seal(a.deliveryEncrypter, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(sealedA), "v1.") {
		t.Fatal("phase (a) sealed a prefixed envelope; with legacy active it must write the single-key shape")
	}
	if _, err := command.Open(before.deliveryEncrypter, sealedA); err != nil {
		t.Fatalf("a pre-ring instance cannot open a phase (a) envelope: %v", err)
	}
	if _, err := command.Open(a.deliveryEncrypter, old); err != nil {
		t.Fatalf("phase (a) cannot open a pre-ring envelope: %v", err)
	}

	b := keyedFor(map[string]string{"AUTH_DELIVERY_ENCRYPTER_KEY": rotationOldKey, "AUTH_DELIVERY_ENCRYPTER_KEYS": "d2:" + rotationNewKey, "AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY": "d2"})
	if got, err := command.Open(b.deliveryEncrypter, old); err != nil || got.ResolutionInput != envelope.ResolutionInput {
		t.Fatalf("phase (b) open of a pre-ring envelope = %+v, %v", got, err)
	}
	sealedB, err := command.Seal(b.deliveryEncrypter, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(sealedB), "v1.d2.") {
		t.Fatal("phase (b) envelope is not sealed under d2")
	}
	if _, err := command.Open(a.deliveryEncrypter, sealedB); err != nil {
		t.Fatalf("a phase (a) instance cannot open a phase (b) envelope: %v", err)
	}

	c := keyedFor(map[string]string{"AUTH_DELIVERY_ENCRYPTER_KEYS": "d2:" + rotationNewKey, "AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY": "d2"})
	if _, err := command.Open(c.deliveryEncrypter, sealedB); err != nil {
		t.Fatalf("phase (c) cannot open a d2 envelope: %v", err)
	}
	_, err = command.Open(c.deliveryEncrypter, old)
	if !errors.Is(err, command.ErrUnsealedPayload) {
		t.Fatalf("phase (c) open of a pre-ring envelope = %v, want ErrUnsealedPayload", err)
	}
	if strings.Contains(err.Error(), string(old)) || strings.Contains(err.Error(), envelope.ResolutionInput) {
		t.Fatal("the refusal carries envelope bytes")
	}
}

// TestTokenRotationReadsPreRingRows proves the provider-token ring reads a row
// the single key sealed, reports it under the legacy id (what the runbook's
// count query finds as a value with no "v1." prefix), and writes new rows under
// the active id.
func TestTokenRotationReadsPreRingRows(t *testing.T) {
	setRotationEnv(t, map[string]string{"AUTH_TOKEN_ENCRYPTER_KEY": rotationOldKey})
	before, err := buildKeyedCollaborators(environment.ModeDevelopment, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	row, err := before.tokenEncrypter.Encrypt("provider-access-token")
	if err != nil {
		t.Fatal(err)
	}
	setRotationEnv(t, map[string]string{"AUTH_TOKEN_ENCRYPTER_KEY": rotationOldKey, "AUTH_TOKEN_ENCRYPTER_KEYS": "t2:" + rotationNewKey, "AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY": "t2"})
	b, err := buildKeyedCollaborators(environment.ModeDevelopment, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	ring, ok := b.tokenEncrypter.(*cryptids.AESGCMRing)
	if !ok {
		t.Fatalf("the token ring built %T, want *cryptids.AESGCMRing", b.tokenEncrypter)
	}
	if got, err := ring.Decrypt(row); err != nil || got != "provider-access-token" {
		t.Fatalf("the ring cannot read a pre-ring row: %v", err)
	}
	if id, err := ring.KeyIDOf(row); err != nil || id != auth.LegacyKeyID {
		t.Fatalf("KeyIDOf(pre-ring row) = %q, %v; want legacy", id, err)
	}
	fresh, err := ring.Encrypt("provider-access-token")
	if err != nil || !strings.HasPrefix(fresh, "v1.t2.") {
		t.Fatalf("a new row is not sealed under t2: %v", err)
	}
	if strings.Contains(row, ".") {
		t.Fatal("a pre-ring row contains a dot; the runbook's count query relies on it never does")
	}
}

// TestPepperRotationKeepsOldCodesVerifying issues email-verification codes on
// an instance with the single AUTH_CHALLENGE_PEPPER (this host's id "dev"),
// then verifies one on an instance whose AUTH_CHALLENGE_PEPPERS ring keeps
// dev beside the new active p2, and proves a ring that has dropped dev
// refuses the other.
func TestPepperRotationKeepsOldCodesVerifying(t *testing.T) {
	repos := authmem.New().Repositories()
	before := bootRotationHost(t, repos, map[string]string{"AUTH_CHALLENGE_PEPPER": rotationOldKey}, nil)
	if got := before.cfg.IdentityConfig.ChallengeProtector.ActiveKeyID(); got != "dev" {
		t.Fatalf("the single pepper's active id = %q, want dev", got)
	}
	codes := map[string]string{}
	for _, email := range []string{"pepper-kept@example.com", "pepper-dropped@example.com"} {
		mark := before.sender.count()
		if resp, body := before.newClient().do("POST", "/auth/register", `{"email":"`+email+`","password":"`+linkPassword+`","display_name":"Pepper"}`, nil); resp.StatusCode != http.StatusCreated {
			t.Fatalf("register %s = %d; body=%s", email, resp.StatusCode, body)
		}
		codes[email] = before.awaitCode(mark, email)
	}

	overlap := bootRotationHost(t, repos, map[string]string{"AUTH_CHALLENGE_PEPPERS": "dev:" + rotationOldKey + ",p2:" + rotationNewKey, "AUTH_CHALLENGE_ACTIVE_PEPPER": "p2"}, nil)
	if got := overlap.cfg.IdentityConfig.ChallengeProtector.ActiveKeyID(); got != "p2" {
		t.Fatalf("the ring's active id = %q, want p2", got)
	}
	if resp, body := overlap.newClient().do("POST", "/auth/verify", `{"email":"pepper-kept@example.com","code":"`+codes["pepper-kept@example.com"]+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a code issued under dev on the dev+p2 ring = %d, want 200; body=%s", resp.StatusCode, body)
	}

	dropped := bootRotationHost(t, repos, map[string]string{"AUTH_CHALLENGE_PEPPERS": "p2:" + rotationNewKey, "AUTH_CHALLENGE_ACTIVE_PEPPER": "p2"}, nil)
	resp, _ := dropped.newClient().do("POST", "/auth/verify", `{"email":"pepper-dropped@example.com","code":"`+codes["pepper-dropped@example.com"]+`"}`, nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a code issued under dev verified after dev left the ring")
	}
}

// TestKeyRotationErrorsCarryNoKey proves a malformed ring is refused naming the
// variable and never echoing the key material.
func TestKeyRotationErrorsCarryNoKey(t *testing.T) {
	setRotationEnv(t, map[string]string{"AUTH_JWT_KEYS": "k2:" + rotationNewKey[:40], "AUTH_JWT_ACTIVE_KEY": "k2"})
	_, err := buildKeyedCollaborators(environment.ModeDevelopment, quietLog())
	if !errors.Is(err, auth.ErrKeyRequired) || !strings.Contains(err.Error(), "AUTH_JWT_KEYS") {
		t.Fatalf("a short ring key = %v, want ErrKeyRequired naming AUTH_JWT_KEYS", err)
	}
	if strings.Contains(err.Error(), rotationNewKey[:40]) {
		t.Fatal("the refusal echoes key material")
	}
}
