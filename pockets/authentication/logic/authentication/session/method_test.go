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
// an auth-v4 method is never silently treated as AAL1.
func TestDescribeMethod_UnknownKind(t *testing.T) {
	if _, ok := DescribeMethod("passkey"); ok {
		t.Error("DescribeMethod(\"passkey\"): ok=true, want false (unregistered)")
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
