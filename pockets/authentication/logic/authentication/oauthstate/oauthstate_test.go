package oauthstate

import (
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestStateTokenIgnoresTheEntityIDs pins the OAuth state token to the package's
// own secret generator. New takes no IDGenerator, so no WithIDs — sdk.DatabaseID
// included — can empty or reshape the one-time flow credential.
func TestStateTokenIgnoresTheEntityIDs(t *testing.T) {
	a := New("google", PurposeFlow, nil, time.Minute, time.Now())
	b := New("google", PurposeFlow, nil, time.Minute, time.Now())
	for _, tok := range []string{a.Token, b.Token} {
		if len(tok) != sdk.DefaultIDLength || strings.Trim(tok, sdk.DefaultIDAlphabet) != "" {
			t.Errorf("state token %q is not a full NanoID", tok)
		}
	}
	if a.Token == b.Token {
		t.Errorf("two state tokens repeated: %q", a.Token)
	}
}
