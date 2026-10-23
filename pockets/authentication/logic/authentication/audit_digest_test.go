package authentication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// auditKeyer returns the bundled HMAC identifier keyer under a fixed test key.
func auditKeyer(t *testing.T) *protection.HMACIdentifierKeyer {
	t.Helper()
	keyer, err := protection.NewHMACIdentifierKeyer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewHMACIdentifierKeyer: %v", err)
	}
	return keyer
}

// assertNoAddressInDetails fails when any recorded row carries an `email` or
// `identifier` Details key, or a Details value holding an `@` or one of
// addresses (AUDIT-054).
func assertNoAddressInDetails(t *testing.T, events []securityevent.SecurityEvent, addresses ...string) {
	t.Helper()
	assertNoSecretInDetails(t, events, append(addresses, "@")...)
	for _, evt := range events {
		for _, key := range []string{"email", "identifier"} {
			if _, ok := evt.Details[key]; ok {
				t.Errorf("%s/%s carries Details[%q]", evt.EventType, evt.EventStatus, key)
			}
		}
	}
}

// lastRow returns the last recorded row of eventType.
func lastRow(t *testing.T, events []securityevent.SecurityEvent, eventType string) securityevent.SecurityEvent {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType == eventType {
			return events[i]
		}
	}
	t.Fatalf("no %s row recorded", eventType)
	return securityevent.SecurityEvent{}
}

// TestLoginRowsCarryIdentifierDigest pins AUDIT-054 for login rows: Details
// carry identifier_kind email and identifier_digest = IdentifierKey(email,
// normalized), or of the trimmed raw input when normalization failed, and no
// row carries the address in any form.
func TestLoginRowsCarryIdentifierDigest(t *testing.T) {
	const address = "digest@example.com"
	keyer := auditKeyer(t)
	for _, tc := range []struct {
		name, attempt, password, digested, status string
	}{
		{"success", "  Digest@Example.COM ", denialPassword, address, securityevent.StatusSuccess},
		{"wrong password", address, "wrong-password-0451", address, securityevent.StatusFailure},
		{"unknown address", "Nobody-0451@Example.com", denialPassword, "nobody-0451@example.com", securityevent.StatusFailure},
		{"malformed address", "  Bob <bob-0451@example.com>  ", denialPassword, "Bob <bob-0451@example.com>", securityevent.StatusFailure},
		{"malformed without an at sign", " not an address 0451 ", denialPassword, "not an address 0451", securityevent.StatusFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTokenHarness(t, newFakeSigner(), false, nil)
			h.svc.identifierKeyer = keyer
			h.mustRegister(t, address, denialPassword)
			_, _, err := h.svc.Login(context.Background(), tc.attempt, tc.password)
			if (err == nil) != (tc.status == securityevent.StatusSuccess) {
				t.Fatalf("Login = %v, want status %s", err, tc.status)
			}
			row := lastRow(t, h.events.recorded(), securityevent.TypeLogin)
			if row.EventStatus != tc.status || row.Details["identifier_kind"] != sdk.AddressKindEmail ||
				row.Details["identifier_digest"] != keyer.IdentifierKey(sdk.AddressKindEmail, tc.digested) {
				t.Fatalf("login row = %s %+v, want identifier_kind email and the keyer digest of %q", row.EventStatus, row.Details, tc.digested)
			}
			assertNoAddressInDetails(t, h.events.recorded(), tc.attempt, strings.TrimSpace(tc.attempt), tc.digested, tc.password)
		})
	}
}

// TestAuditDigestFallback pins the development fallback with no keyer wired:
// SHA-256 of kind + ":" + value, never the input — not even for an empty value,
// where identifierDigest would return its raw input.
func TestAuditDigestFallback(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	h.mustRegister(t, "fallback@example.com", denialPassword)
	if _, _, err := h.svc.Login(context.Background(), " Fallback@Example.com ", "wrong-password-0451"); err == nil {
		t.Fatal("Login succeeded with a wrong password")
	}
	sum := sha256.Sum256([]byte("email:fallback@example.com"))
	row := lastRow(t, h.events.recorded(), securityevent.TypeLogin)
	if row.Details["identifier_digest"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("fallback digest = %v, want sha256(email:fallback@example.com)", row.Details["identifier_digest"])
	}
	assertNoAddressInDetails(t, h.events.recorded(), "fallback@example.com", "Fallback@Example.com")
	for _, value := range []string{"", "x@example.com"} {
		if got := h.svc.auditDigest("", value); len(got) != 64 || got == value || strings.Contains(got, "@") {
			t.Errorf("auditDigest(%q) = %q, want a 64-char hex digest", value, got)
		}
	}
}
