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
	} {
		if tc.got != tc.want {
			t.Errorf("event type = %q, want %q", tc.got, tc.want)
		}
	}
}

// TestEventTypesAreRegistered pins the registry: every Type* constant declared
// in this package is listed in Types exactly once, every value is unique and
// snake_case, and the two promoted session literals keep the values rows were
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
