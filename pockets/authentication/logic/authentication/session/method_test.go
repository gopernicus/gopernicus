package session

import "testing"

// TestDescribeMethod_AllV3MethodsAAL1 proves the honest v3 vocabulary: every
// bundled method is AAL1 and none overclaims phishing or replay resistance, so
// policy cannot mistake a single-factor method for a stronger one.
func TestDescribeMethod_AllV3MethodsAAL1(t *testing.T) {
	kinds := []MethodKind{MethodPassword, MethodEmailLink, MethodEmailCode, MethodSMSCode, MethodOAuth}
	for _, k := range kinds {
		d, ok := DescribeMethod(k)
		if !ok {
			t.Errorf("DescribeMethod(%q): ok=false, want a descriptor", k)
			continue
		}
		if d.Assurance != AssuranceAAL1 {
			t.Errorf("%q assurance = %q, want %q", k, d.Assurance, AssuranceAAL1)
		}
		if d.PhishingResistant {
			t.Errorf("%q claims phishing resistance; no v3 method is phishing-resistant", k)
		}
		if d.ReplayResistant {
			t.Errorf("%q claims replay resistance; no v3 method is replay-resistant", k)
		}
	}
}

// TestDescribeMethod_SMSisPSTN proves SMS is classified PSTN (restricted) while
// non-SMS methods are not — the property policy uses to restrict the channel.
func TestDescribeMethod_SMSisPSTN(t *testing.T) {
	if d, _ := DescribeMethod(MethodSMSCode); !d.PSTN {
		t.Error("MethodSMSCode.PSTN = false, want true")
	}
	if d, _ := DescribeMethod(MethodPassword); d.PSTN {
		t.Error("MethodPassword.PSTN = true, want false")
	}
}

// TestDescribeMethod_UnknownKind proves an unregistered kind reports ok=false so
// a future method is never silently treated as AAL1; MethodExternal is evidence
// on a Credential, never a session method, so it has no descriptor either.
func TestDescribeMethod_UnknownKind(t *testing.T) {
	for _, kind := range []MethodKind{"sms_push", MethodExternal} {
		if _, ok := DescribeMethod(kind); ok {
			t.Errorf("DescribeMethod(%q): ok=true, want false (unregistered)", kind)
		}
	}
}

// TestDescribeMethod_SecondFactors pins TOTP and recovery codes: AAL1 on their
// own (a step-up composes AAL2 onto a live session), replay-resistant, not
// phishing-resistant, not PSTN.
func TestDescribeMethod_SecondFactors(t *testing.T) {
	for _, kind := range []MethodKind{MethodTOTP, MethodRecoveryCode} {
		d, ok := DescribeMethod(kind)
		if !ok || d.Assurance != AssuranceAAL1 || d.PhishingResistant || !d.ReplayResistant || d.PSTN {
			t.Errorf("DescribeMethod(%q) = %+v, %v", kind, d, ok)
		}
	}
}

// TestDescribeMethod_Passkeys pins the only native AAL2 source: a user-verified
// passkey is AAL2, a presence-only one AAL1, and both resist phishing and replay.
func TestDescribeMethod_Passkeys(t *testing.T) {
	for kind, want := range map[MethodKind]AssuranceLevel{MethodPasskey: AssuranceAAL2, MethodPasskeyUnverified: AssuranceAAL1} {
		d, ok := DescribeMethod(kind)
		if !ok || d.Assurance != want || !d.PhishingResistant || !d.ReplayResistant || d.PSTN {
			t.Errorf("DescribeMethod(%q) = %+v, %v; want %s, phishing- and replay-resistant", kind, d, ok, want)
		}
	}
}

// TestAssuranceAtLeast pins the one comparator: unknown < aal1 < aal2 < aal3,
// and an unrecognised level ranks as unknown.
func TestAssuranceAtLeast(t *testing.T) {
	levels := []AssuranceLevel{AssuranceUnknown, AssuranceAAL1, AssuranceAAL2, AssuranceAAL3}
	for i, a := range levels {
		for j, min := range levels {
			if got, want := a.AtLeast(min), i >= j; got != want {
				t.Errorf("%q.AtLeast(%q) = %v, want %v", a, min, got, want)
			}
		}
	}
	for _, tc := range []struct {
		a, min AssuranceLevel
		want   bool
	}{
		{"aal9", AssuranceAAL1, false},
		{"aal9", AssuranceUnknown, true},
		{AssuranceAAL1, "aal9", true},
		{"AAL2", AssuranceAAL2, false},
	} {
		if got := tc.a.AtLeast(tc.min); got != tc.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", tc.a, tc.min, got, tc.want)
		}
	}
}
