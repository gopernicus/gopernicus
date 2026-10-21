// Package authenticationtest is a conformance suite for authentication.Provider
// and authentication.RevocationChecker implementations, in the cachertest /
// eventstest style: RunProvider(t, newProvider, mint) and RunRevocation pin the
// shared contract every implementation must honor. Imports stdlib + sdk +
// sdk/capabilities/authentication only, so a suite over a fake verifier needs
// no network.
//
// That a provider rejects an oversize credential WITHOUT calling its verifier
// is not observable through the Provider port; implementations pin it in their
// own tests (NewOIDC does).
package authenticationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// conformanceSubject is the sub every minted suite token carries.
const conformanceSubject = "authenticationtest-subject"

// Minter returns a credential carrying claims, signed so that the provider's
// verifier accepts it on signature alone. It adds any claim the verifier pins
// that the suite omits (aud, kid) and never overrides a claim the suite sets:
// the suite relies on minting foreign-issuer and expired tokens.
type Minter func(t *testing.T, claims map[string]any) string

// RunProvider exercises the Provider contract against a fresh provider per subtest.
func RunProvider(t *testing.T, newProvider func(t *testing.T) authentication.Provider, mint Minter) {
	t.Helper()

	t.Run("ValidCredential", func(t *testing.T) { testValid(t, newProvider(t), mint) })
	t.Run("WrongIssuer", func(t *testing.T) { testWrongIssuer(t, newProvider(t), mint) })
	t.Run("Expired", func(t *testing.T) { testExpired(t, newProvider(t), mint) })
	t.Run("Oversize", func(t *testing.T) { testOversize(t, newProvider(t), mint) })
	t.Run("EmptyAndMalformed", func(t *testing.T) { testMalformed(t, newProvider(t)) })
	t.Run("MethodsCloned", func(t *testing.T) { testMethodsCloned(t, newProvider(t), mint) })
}

// RunRevocation exercises the RevocationChecker contract. knownSubject must be
// an existing subject of issuer; the suite invents an unknown one.
func RunRevocation(t *testing.T, newChecker func(t *testing.T) authentication.RevocationChecker, issuer, knownSubject string) {
	t.Helper()

	t.Run("KnownSubject", func(t *testing.T) {
		facts, err := newChecker(t).CheckRevocation(context.Background(), issuer, knownSubject)
		if err != nil {
			t.Fatalf("CheckRevocation(known) error = %v", err)
		}
		if facts.CheckedAt.IsZero() {
			t.Error("CheckRevocation(known) CheckedAt is zero")
		}
	})
	t.Run("UnknownSubjectNeverValid", func(t *testing.T) {
		facts, err := newChecker(t).CheckRevocation(context.Background(), issuer, "authenticationtest-unknown-"+randomHex(t))
		if err != nil {
			if !errors.Is(err, sdk.ErrNotFound) {
				t.Fatalf("CheckRevocation(unknown) error = %v, want sdk.ErrNotFound or Disabled facts", err)
			}
			return
		}
		if !facts.Disabled {
			t.Fatalf("CheckRevocation(unknown) = %+v, nil; an unknown subject must be Disabled or sdk.ErrNotFound", facts)
		}
	})
	t.Run("EmptySubjectNeverValid", func(t *testing.T) {
		facts, err := newChecker(t).CheckRevocation(context.Background(), issuer, "")
		if err == nil && !facts.Disabled {
			t.Fatalf("CheckRevocation(empty) = %+v, nil; an empty subject must never read as valid", facts)
		}
	})
}

func validClaims(issuer string, now time.Time) map[string]any {
	return map[string]any{
		"iss":            issuer,
		"sub":            conformanceSubject,
		"iat":            now.Add(-time.Minute).Unix(),
		"auth_time":      now.Add(-2 * time.Minute).Unix(),
		"exp":            now.Add(10 * time.Minute).Unix(),
		"amr":            []any{"pwd", "mfa"},
		"email":          "subject@example.test",
		"email_verified": true,
	}
}

func authenticate(p authentication.Provider, raw string) (authentication.Identity, error) {
	return p.Authenticate(context.Background(), authentication.Credential{Transport: authentication.TransportHeader, Raw: raw})
}

func testValid(t *testing.T, p authentication.Provider, mint Minter) {
	now := time.Now()
	claims := validClaims(p.Issuer(), now)
	id, err := authenticate(p, mint(t, claims))
	if err != nil {
		t.Fatalf("Authenticate(valid) error = %v", err)
	}
	if id.Issuer != p.Issuer() {
		t.Errorf("Identity.Issuer = %q, want Issuer() %q", id.Issuer, p.Issuer())
	}
	if id.Subject != conformanceSubject {
		t.Errorf("Identity.Subject = %q, want %q", id.Subject, conformanceSubject)
	}
	wantExp := time.Unix(claims["exp"].(int64), 0)
	if d := id.ExpiresAt.Sub(wantExp); d < -time.Second || d > time.Second {
		t.Errorf("Identity.ExpiresAt = %v, want %v", id.ExpiresAt, wantExp)
	}
	if id.AuthenticatedAt.IsZero() {
		t.Error("Identity.AuthenticatedAt is zero; want auth_time, else iat")
	}
}

func testWrongIssuer(t *testing.T, p authentication.Provider, mint Minter) {
	claims := validClaims(p.Issuer()+"-foreign", time.Now())
	id, err := authenticate(p, mint(t, claims))
	if !errors.Is(err, authentication.ErrIssuerMismatch) {
		t.Fatalf("Authenticate(foreign iss) error = %v, want ErrIssuerMismatch", err)
	}
	assertRejected(t, id, err)
}

func testExpired(t *testing.T, p authentication.Provider, mint Minter) {
	now := time.Now()
	claims := validClaims(p.Issuer(), now)
	claims["iat"] = now.Add(-2 * time.Hour).Unix()
	claims["auth_time"] = now.Add(-2 * time.Hour).Unix()
	claims["exp"] = now.Add(-time.Hour).Unix()
	id, err := authenticate(p, mint(t, claims))
	assertRejected(t, id, err)
}

// testOversize presents an otherwise valid token past MaxCredentialBytes, so a
// provider without the cap accepts it and fails.
func testOversize(t *testing.T, p authentication.Provider, mint Minter) {
	claims := validClaims(p.Issuer(), time.Now())
	claims["pad"] = strings.Repeat("a", authentication.MaxCredentialBytes)
	id, err := authenticate(p, mint(t, claims))
	assertRejected(t, id, err)
}

func testMalformed(t *testing.T, p authentication.Provider) {
	for _, raw := range []string{"", "not-a-token", "a.b", "a.b.c.d.e", "!!!.***.???"} {
		id, err := authenticate(p, raw)
		assertRejected(t, id, err)
	}
}

func testMethodsCloned(t *testing.T, p authentication.Provider, mint Minter) {
	raw := mint(t, validClaims(p.Issuer(), time.Now()))
	first, err := authenticate(p, raw)
	if err != nil {
		t.Fatalf("Authenticate(first) error = %v", err)
	}
	if len(first.Methods) == 0 {
		t.Skip("provider maps no authentication methods; nothing to alias")
	}
	want := append([]string(nil), first.Methods...)
	first.Methods[0] = "mutated"
	second, err := authenticate(p, raw)
	if err != nil {
		t.Fatalf("Authenticate(second) error = %v", err)
	}
	if !reflect.DeepEqual(second.Methods, want) {
		t.Fatalf("second Identity.Methods = %v, want %v; a returned slice must not alias provider state", second.Methods, want)
	}
}

// assertRejected pins the rejection contract: an invalid-credential error that
// is never an outage, and no fabricated Identity.
func assertRejected(t *testing.T, id authentication.Identity, err error) {
	t.Helper()
	if !errors.Is(err, authentication.ErrInvalidCredential) {
		t.Fatalf("error = %v, want ErrInvalidCredential", err)
	}
	if !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("error = %v, want it to match sdk.ErrUnauthorized", err)
	}
	if errors.Is(err, sdk.ErrUnavailable) {
		t.Errorf("error = %v matches sdk.ErrUnavailable; a rejected credential is not an outage", err)
	}
	if !reflect.DeepEqual(id, authentication.Identity{}) {
		t.Errorf("Identity = %+v on error, want zero", id)
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}
