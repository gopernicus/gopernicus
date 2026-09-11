package authenticationhttp

import (
	"context"
	"net/url"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
)

type channelInventory struct{ authService }

func (channelInventory) Methods(context.Context, string) (authlogic.MethodsView, error) {
	return authlogic.MethodsView{Identifiers: []authlogic.IdentifierMethodView{
		{Kind: "email", MaskedValue: "unverified@example.com", Uses: credential.IdentifierUses{Recovery: true}},
		{Kind: "phone", MaskedValue: "+1******1234", VerifiedAt: time.Now(), Uses: credential.IdentifierUses{Recovery: true}},
	}}, nil
}

func TestStepUpKeepsSelectedRecoveryChannel(t *testing.T) {
	h := handlers{svc: channelInventory{}}
	m := h.stepUpModel(context.Background(), "user", PageContext{}, stepUpParams{purpose: "sensitive", context: "resource"})
	if !m.CodeAvailable || m.Kind != "phone" || m.MaskedIdentifier != "+1******1234" {
		t.Fatalf("page selected an unusable recovery channel: %+v", m)
	}
	dest, err := url.Parse(stepUpPath(stepUpParams{kind: m.Kind, purpose: m.Purpose, context: m.Context, sent: true}))
	if err != nil {
		t.Fatal(err)
	}
	if dest.Query().Get("kind") != "phone" || dest.Query().Get("context") != "resource" {
		t.Fatalf("post-issuance redirect lost proof binding: %s", dest)
	}
	m = h.stepUpModel(context.Background(), "user", PageContext{}, stepUpParams{kind: "email"})
	if m.CodeAvailable {
		t.Fatal("explicit unverified channel fell back to a different channel")
	}
}
