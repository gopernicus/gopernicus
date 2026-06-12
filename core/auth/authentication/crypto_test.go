package authentication

import "testing"

// HashToken is the exported seam test harnesses use to seed credential rows
// the engine will match — it must stay identical to the internal hash.
func TestHashTokenMatchesInternal(t *testing.T) {
	exported, err := HashToken("some-token")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := hashToken("some-token")
	if err != nil {
		t.Fatal(err)
	}
	if exported != internal {
		t.Errorf("HashToken = %q, hashToken = %q — the exported seam drifted", exported, internal)
	}

	if _, err := HashToken(""); err == nil {
		t.Error("empty token must error")
	}
}
