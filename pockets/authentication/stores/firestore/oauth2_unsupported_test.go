package firestore

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestSessionDocumentRejectsUnsupportedDelegation(t *testing.T) {
	sess, _ := session.NewSession("user", time.Hour, time.Now())
	if _, err := newSessionDoc(sess); err != nil {
		t.Fatal(err)
	}
	sess.Profile = session.ProfileDelegated
	sess.Delegation = session.Delegation{Issuer: "https://issuer.example", ClientID: "client", Resource: "https://mcp.example"}
	if _, err := newSessionDoc(sess); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("delegated session silently lost its binding: %v", err)
	}
	sess.Profile = session.ProfileFirstParty
	if _, err := newSessionDoc(sess); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("mixed first-party binding admitted: %v", err)
	}
}
