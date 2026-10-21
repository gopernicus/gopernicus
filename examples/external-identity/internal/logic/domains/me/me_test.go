package me

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

type fakeResolver struct {
	info sdk.IdentityInfo
	err  error
}

func (f fakeResolver) Resolve(context.Context, sdk.Principal) (sdk.IdentityInfo, error) {
	return f.info, f.err
}

func TestGetProjectsTheResolvedIdentity(t *testing.T) {
	p := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "local-1"}
	svc := NewService(fakeResolver{info: sdk.IdentityInfo{Principal: p, DisplayName: "Ada", Addresses: []sdk.IdentityAddress{{Kind: "email", Value: "a@x.test"}}}})
	got, err := svc.Get(context.Background(), p)
	if err != nil || got.PrincipalID != "local-1" || got.DisplayName != "Ada" || len(got.Addresses) != 1 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func TestGetNeverReturnsNilAddresses(t *testing.T) {
	got, err := NewService(fakeResolver{}).Get(context.Background(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u"})
	if err != nil || got.Addresses == nil || len(got.Addresses) != 0 {
		t.Fatalf("Get = %+v, %v; want an empty non-nil address list", got, err)
	}
}

func TestGetDeniesAnIncompletePrincipal(t *testing.T) {
	if _, err := NewService(fakeResolver{}).Get(context.Background(), sdk.Principal{}); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("Get(empty) error = %v; want ErrUnauthorized", err)
	}
}

func TestGetPassesResolverErrors(t *testing.T) {
	if _, err := NewService(fakeResolver{err: sdk.ErrNotFound}).Get(context.Background(), sdk.Principal{Type: "user", ID: "u"}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Get error = %v; want ErrNotFound", err)
	}
}
