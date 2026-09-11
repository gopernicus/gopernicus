package passwordreset

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestResetBindingRequiresCurrentRecoveryProof(t *testing.T) {
	b := Binding{Version: BindingVersion, AuthRevision: 4, IdentifierID: "recovery"}
	ident := identifier.Identifier{ID: "recovery", UserID: "owner", VerifiedAt: time.Now(), RecoveryEnabled: true}
	if !b.Matches("owner", 4, ident) {
		t.Fatal("current recovery proof rejected")
	}
	for _, test := range []struct {
		name   string
		change func(*identifier.Identifier)
	}{
		{"different_identifier", func(i *identifier.Identifier) { i.ID = "replacement" }},
		{"different_owner", func(i *identifier.Identifier) { i.UserID = "other" }},
		{"retired", func(i *identifier.Identifier) { i.ReplacedAt = time.Now() }},
		{"unverified", func(i *identifier.Identifier) { i.VerifiedAt = time.Time{} }},
		{"recovery_disabled", func(i *identifier.Identifier) { i.RecoveryEnabled = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := ident
			test.change(&current)
			if b.Matches("owner", 4, current) {
				t.Fatal("stale recovery proof accepted")
			}
		})
	}
	if b.Matches("owner", 5, ident) {
		t.Fatal("proof rebased across credential revision")
	}
	for _, raw := range []json.RawMessage{nil, []byte(`{}`), []byte(`{"version":2,"identifier_id":"recovery"}`), []byte(`{"version":1,"identifier_id":"recovery","auth_revision":-1}`)} {
		if _, err := ParseBinding(raw); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("invalid binding %s: %v", raw, err)
		}
	}
}
