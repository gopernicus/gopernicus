package securityevent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestMachineLifecycleTypesAreStableWireValues pins the machine-identity
// lifecycle vocabulary: event_type is free TEXT at rest (no CHECK in either
// dialect), so a rename here silently orphans every persisted row and every
// operator filter rather than failing a migration.
func TestMachineLifecycleTypesAreStableWireValues(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{TypeServiceAccountCreated, "service_account_created"},
		{TypeAPIKeyMinted, "api_key_minted"},
		{TypeAPIKeyRevoked, "api_key_revoked"},
	} {
		if tc.got != tc.want {
			t.Errorf("event type = %q, want %q", tc.got, tc.want)
		}
	}
	// The lifecycle types are distinct from the credential-USE event.
	seen := map[string]bool{}
	for _, v := range []string{TypeAPIKeyAuth, TypeServiceAccountCreated, TypeAPIKeyMinted, TypeAPIKeyRevoked} {
		if seen[v] {
			t.Errorf("duplicate event type %q in the machine vocabulary", v)
		}
		seen[v] = true
	}
}

// TestExternalIdentityTypesAreStableWireValues pins the external identity
// vocabulary for the same reason: event_type is free TEXT at rest.
func TestExternalIdentityTypesAreStableWireValues(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{TypeExternalSubjectProvisioned, "external_subject_provisioned"},
		{TypeExternalSubjectUnknown, "external_subject_unknown"},
		{TypeExternalSubjectCollision, "external_subject_collision"},
		{TypeExternalSubjectDenied, "external_subject_denied"},
		{TypeExternalRevocationUnavailable, "external_revocation_unavailable"},
		{TypeMFARequiredDenied, "mfa_required_denied"},
	} {
		if tc.got != tc.want {
			t.Errorf("event type = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestEventTypesAreRegistered pins the registry: every Type* constant declared
// in this package is listed in Types exactly once, every value is unique and
// snake_case, and the promoted session literals and the retention vocabulary
// keep the values rows were
// written with — event_type is free TEXT at rest, so a rename orphans rows.
func TestEventTypesAreRegistered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, ident := range vs.Names {
					if !strings.HasPrefix(ident.Name, "Type") || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s is not a string literal", ident.Name)
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					declared[ident.Name] = value
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no Type* constants")
	}

	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	registered := map[string]int{}
	for _, v := range Types() {
		registered[v]++
		if !snake.MatchString(v) {
			t.Errorf("event type %q is not snake_case", v)
		}
	}
	values := map[string]string{}
	for name, v := range declared {
		if other, dup := values[v]; dup {
			t.Errorf("%s and %s share the value %q", name, other, v)
		}
		values[v] = name
		switch registered[v] {
		case 0:
			t.Errorf("%s (%q) is not listed in Types()", name, v)
		case 1:
		default:
			t.Errorf("%s (%q) is listed %d times in Types()", name, v, registered[v])
		}
	}
	for v := range registered {
		if _, ok := values[v]; !ok {
			t.Errorf("Types() lists %q, which no Type* constant declares", v)
		}
	}
	if len(Types()) != len(declared) {
		t.Errorf("Types() has %d entries, want %d", len(Types()), len(declared))
	}

	for _, tc := range []struct{ got, want string }{
		{TypeSessionRevoked, "session_revoked"},
		{TypeAllSessionsRevoked, "all_sessions_revoked"},
		{TypeUserErased, "user_erased"},
		{TypeServiceAccountDeleted, "service_account_deleted"},
		{TypeSecurityEventsPurged, "security_events_purged"},
		{TypeSecurityEventsArchived, "security_events_archived"},
	} {
		if tc.got != tc.want {
			t.Errorf("event type = %q, want %q", tc.got, tc.want)
		}
	}

	clone := Types()
	clone[0] = "mutated"
	if Types()[0] == "mutated" {
		t.Error("Types() exposes the registry's backing array")
	}
}

// TestReasonsAreStableWireValues pins the denial reason vocabulary: reasons
// are free TEXT inside Details, so a rename silently splits every operator
// filter. Each value is unique and snake_case.
func TestReasonsAreStableWireValues(t *testing.T) {
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	seen := map[string]bool{}
	for _, tc := range []struct{ got, want string }{
		{ReasonInvalidCredentials, "invalid_credentials"},
		{ReasonRateLimited, "rate_limited"},
		{ReasonInactive, "inactive"},
		{ReasonEmailUnverified, "email_unverified"},
		{ReasonInvalidToken, "invalid_token"},
		{ReasonInvalidCode, "invalid_code"},
		{ReasonLockout, "lockout"},
		{ReasonPolicy, "policy"},
		{ReasonInvalidState, "invalid_state"},
		{ReasonExchangeFailed, "exchange_failed"},
		{ReasonIdentityInvalid, "identity_invalid"},
		{ReasonLinkConflict, "link_conflict"},
		{ReasonInvalidGrant, "invalid_grant"},
		{ReasonInvalidClient, "invalid_client"},
		{ReasonRevisionConflict, "revision_conflict"},
		{ReasonPrecondition, "precondition"},
		{ReasonClaimConflict, "claim_conflict"},
		{ReasonUnknownProvider, "unknown_provider"},
		{ReasonInvalidRequest, "invalid_request"},
		{ReasonInvalidScope, "invalid_scope"},
		{ReasonInvalidTarget, "invalid_target"},
	} {
		if tc.got != tc.want {
			t.Errorf("reason = %q, want %q", tc.got, tc.want)
		}
		if !snake.MatchString(tc.got) || seen[tc.got] {
			t.Errorf("reason %q is not unique snake_case", tc.got)
		}
		seen[tc.got] = true
	}
	for _, tc := range []struct{ got, want string }{
		{TypeOAuthPendingLinkSent, "oauth_pending_link_sent"},
		{TypeExternalLogin, "external_login"},
		{TypeOAuth2ConsentDenied, "oauth2_consent_denied"},
		{TypeOAuth2GrantFailed, "oauth2_grant_failed"},
	} {
		if tc.got != tc.want {
			t.Errorf("event type = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestBoundValuePerField pins the ONE attribution rule, field by field: the
// correlation ids keep S2's printable-ASCII rule; the IP, the User-Agent and a
// Details value are blank on a NUL, invalid UTF-8 or a control character; the
// User-Agent alone is cut, on a rune boundary; an unparsable IP and an
// oversized Details value are blank.
func TestBoundValuePerField(t *testing.T) {
	ua511 := strings.Repeat("u", MaxUserAgentLen-1)
	for _, tc := range []struct {
		name  string
		field Field
		value string
		want  string
	}{
		{"request id kept at 128 bytes", FieldRequestID, strings.Repeat("r", MaxCorrelationIDLen), strings.Repeat("r", MaxCorrelationIDLen)},
		{"request id blank at 129 bytes", FieldRequestID, strings.Repeat("r", MaxCorrelationIDLen+1), ""},
		{"request id blank on a control byte", FieldRequestID, "req\n1", ""},
		{"request id blank on DEL", FieldRequestID, "req\x7f", ""},
		{"request id blank on non-ASCII", FieldRequestID, "req-é", ""},
		{"request id keeps printable edges", FieldRequestID, " ~", " ~"},
		{"request id empty", FieldRequestID, "", ""},
		{"trace id kept", FieldTraceID, "trace-1", "trace-1"},
		{"trace id blank at 129 bytes", FieldTraceID, strings.Repeat("t", MaxCorrelationIDLen+1), ""},
		{"trace id blank on non-ASCII", FieldTraceID, "trace-é", ""},

		{"IPv4 kept", FieldIPAddress, "203.0.113.7", "203.0.113.7"},
		{"IPv6 kept", FieldIPAddress, "2001:db8::1", "2001:db8::1"},
		{"IP with a port blank", FieldIPAddress, "203.0.113.7:443", ""},
		{"IP not an address blank", FieldIPAddress, "not-an-address", ""},
		{"IP with a NUL blank", FieldIPAddress, "203.0.113.7\x00", ""},
		{"IP zone with invalid UTF-8 blank", FieldIPAddress, "fe80::1%\xff", ""},
		{"IP empty", FieldIPAddress, "", ""},

		{"UA kept", FieldUserAgent, "Mozilla/5.0 (X11; Linux) café", "Mozilla/5.0 (X11; Linux) café"},
		{"UA kept at 512 bytes", FieldUserAgent, strings.Repeat("u", MaxUserAgentLen), strings.Repeat("u", MaxUserAgentLen)},
		{"UA cut at 512 bytes", FieldUserAgent, strings.Repeat("u", MaxUserAgentLen+100), strings.Repeat("u", MaxUserAgentLen)},
		{"UA cut before a straddling rune", FieldUserAgent, ua511 + "é", ua511},
		{"UA cut before a straddling 4-byte rune", FieldUserAgent, strings.Repeat("u", MaxUserAgentLen-2) + "😀", strings.Repeat("u", MaxUserAgentLen-2)},
		{"UA blank on invalid UTF-8", FieldUserAgent, "agent/\xff", ""},
		{"UA blank on a NUL", FieldUserAgent, "agent\x00/1", ""},
		{"UA blank on a tab", FieldUserAgent, "agent\t/1", ""},
		{"UA blank on a C1 control", FieldUserAgent, "agent\u0085/1", ""},
		{"UA blank on invalid UTF-8 past the cut", FieldUserAgent, strings.Repeat("u", MaxUserAgentLen+10) + "\xff", ""},
		{"UA empty", FieldUserAgent, "", ""},

		{"detail kept", FieldDetail, "https://client.example/metadata", "https://client.example/metadata"},
		{"detail kept non-ASCII", FieldDetail, "café", "café"},
		{"detail kept at 2048 bytes", FieldDetail, strings.Repeat("d", MaxDetailLen), strings.Repeat("d", MaxDetailLen)},
		{"detail blank at 2049 bytes", FieldDetail, strings.Repeat("d", MaxDetailLen+1), ""},
		{"detail blank on a NUL", FieldDetail, "mcp\x00", ""},
		{"detail blank on invalid UTF-8", FieldDetail, "mcp\xff", ""},
		{"detail blank on a newline", FieldDetail, "mcp\nforged=1", ""},
		{"detail empty", FieldDetail, "", ""},

		{"zero field blank", Field(0), "value", ""},
		{"unknown field blank", Field(200), "value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BoundValue(tc.field, tc.value); got != tc.want {
				t.Errorf("BoundValue = %.60q (%d bytes), want %.60q (%d bytes)", got, len(got), tc.want, len(tc.want))
			}
		})
	}
}
