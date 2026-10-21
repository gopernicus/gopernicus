package securityevent

import "testing"

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
