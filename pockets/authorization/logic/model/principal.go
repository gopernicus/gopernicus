package model

import (
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// PrincipalRef is a concrete decision caller or actor — always a (Type, ID)
// pair, NEVER a userset. It is the only subject a decision request carries: a
// userset relation cannot be expressed here, so no public decision path can
// smuggle one in.
//
// Deprecated: PrincipalRef is sdkauthorization.Principal, which is
// sdk.Principal; use that name. The alias is removed in a later minor.
type PrincipalRef = sdkauthorization.Principal

// ValidatePrincipal reports whether the principal is structurally usable: both
// Type and ID must be present and well formed (see tuples.ValidateRefField).
func ValidatePrincipal(p PrincipalRef) error {
	if err := tuples.ValidateRefField("principal type", p.Type); err != nil {
		return err
	}
	return tuples.ValidateRefField("principal id", p.ID)
}
