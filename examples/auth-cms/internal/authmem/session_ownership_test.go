package authmem

import (
	"context"
	"testing"
	"time"

	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

func TestSessionProofMethodsAreDetached(t *testing.T) {
	ctx := context.Background()
	r := New().Repositories()
	original, _ := session.NewSession("owner", time.Hour, time.Now())
	original.RefreshTokenHash = "owned-methods"
	original.Authentication.Methods = []session.AuthenticationMethod{{Kind: session.MethodPassword, Assurance: session.AssuranceAAL1}}
	created, err := r.Sessions.Create(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	original.Authentication.Methods[0].Kind = session.MethodEmailLink
	created.Authentication.Methods[0].Kind = session.MethodSMSLink
	got, err := r.Sessions.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Authentication.Methods[0].Kind != session.MethodPassword {
		t.Fatal("caller changed stored authentication proof")
	}
	got.Authentication.Methods[0].Kind = session.MethodSMSCode
	byHash, _, err := r.Sessions.GetByRefreshHash(ctx, original.RefreshTokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if byHash.Authentication.Methods[0].Kind != session.MethodPassword {
		t.Fatal("read changed stored authentication proof")
	}
	byHash.Authentication.Methods[0].Kind = session.MethodEmailCode
	got, err = r.Sessions.Get(ctx, created.ID)
	if err != nil || got.Authentication.Methods[0].Kind != session.MethodPassword {
		t.Fatalf("refresh lookup leaked proof ownership: %v", err)
	}
}
