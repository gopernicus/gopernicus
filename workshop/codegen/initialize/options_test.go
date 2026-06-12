package initialize

import "testing"

// Authentication implies authorization — an authentication-only scaffold
// does not compile (generated bridges wire both engines).
func TestParseFeaturesFlagCouplesAuthorization(t *testing.T) {
	f, err := ParseFeaturesFlag("authentication")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Authentication || !f.Authorization {
		t.Errorf("authentication=%v authorization=%v, want both true", f.Authentication, f.Authorization)
	}

	f, err = ParseFeaturesFlag("tenancy")
	if err != nil {
		t.Fatal(err)
	}
	if f.Authorization {
		t.Error("tenancy alone must not enable authorization")
	}
}
