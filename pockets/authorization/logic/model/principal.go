package model

import (
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// PrincipalRef is a concrete decision caller or actor — always a (Type, ID)
// pair, NEVER a userset. It is the only subject a decision request carries: a
// userset relation cannot be expressed here, so no public decision path can
// smuggle one in. It mirrors sdk.Principal field-for-field and is directly
// convertible from it (see authorization.PrincipalFrom).
type PrincipalRef struct {
	Type string // "user" or "service_account" (the runtime principal types)
	ID   string
}

// Validate reports whether the principal is structurally usable: both Type and
// ID must be present and well formed (see tuples.ValidateRefField).
func (p PrincipalRef) Validate() error {
	if err := tuples.ValidateRefField("principal type", p.Type); err != nil {
		return err
	}
	return tuples.ValidateRefField("principal id", p.ID)
}
