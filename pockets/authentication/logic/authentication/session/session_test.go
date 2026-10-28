package session

import (
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func isSecret(s string) bool {
	return len(s) == sdk.DefaultIDLength && strings.Trim(s, sdk.DefaultIDAlphabet) == ""
}

// TestSessionSecretsIgnoreTheEntityIDs pins session ids and refresh tokens to
// the package's own secret generator. The constructors take no IDGenerator, so
// no WithIDs — sdk.DatabaseID included — can empty or reshape a credential.
func TestSessionSecretsIgnoreTheEntityIDs(t *testing.T) {
	s, refresh := NewSession("u1", time.Hour, time.Now())
	if !isSecret(s.ID) || !isSecret(refresh) || s.ID == refresh {
		t.Errorf("session id %q / refresh token %q are not distinct full NanoIDs", s.ID, refresh)
	}
	if rotated := NewRefreshToken(); !isSecret(rotated) || rotated == refresh {
		t.Errorf("rotated refresh token %q is not a fresh full NanoID", rotated)
	}
	delegated := NewDelegatedRefreshToken()
	if rest, ok := strings.CutPrefix(delegated, delegatedRefreshTokenPrefix); !ok || !isSecret(rest) {
		t.Errorf("delegated refresh token %q is not the prefix plus a full NanoID", delegated)
	}
}
