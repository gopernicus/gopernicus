// Package me answers "who am I" for an authenticated caller: the local
// principal the identity owner mapped the presented credential to, plus the
// display name and verified addresses it holds for that principal. It knows no
// identity provider; the principal arrives already resolved.
package me

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Profile is the caller's local identity. Addresses holds verified addresses
// only; an address the provider did not verify is never projected.
type Profile struct {
	PrincipalType string    `json:"principal_type"`
	PrincipalID   string    `json:"principal_id"`
	DisplayName   string    `json:"display_name"`
	Addresses     []Address `json:"addresses"`
}

// Address is one verified contact address.
type Address struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Service projects the caller's profile through the identity owner.
type Service struct {
	identities sdk.IdentityResolver
}

// NewService returns a Service over identities. It panics on nil, a wiring
// error.
func NewService(identities sdk.IdentityResolver) *Service {
	if identities == nil {
		panic("me.NewService: identity resolver is required")
	}
	return &Service{identities: identities}
}

// Get returns p's profile. An incomplete principal is sdk.ErrUnauthorized.
func (s *Service) Get(ctx context.Context, p sdk.Principal) (Profile, error) {
	if p.Type == "" || p.ID == "" {
		return Profile{}, fmt.Errorf("me: no authenticated principal: %w", sdk.ErrUnauthorized)
	}
	info, err := s.identities.Resolve(ctx, p)
	if err != nil {
		return Profile{}, err
	}
	addresses := make([]Address, 0, len(info.Addresses))
	for _, a := range info.Addresses {
		addresses = append(addresses, Address{Kind: a.Kind, Value: a.Value})
	}
	return Profile{PrincipalType: p.Type, PrincipalID: p.ID, DisplayName: info.DisplayName, Addresses: addresses}, nil
}
