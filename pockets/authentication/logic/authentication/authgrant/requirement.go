package authgrant

import (
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// Requirement identifies one operation and the oldest/weakest proof it accepts.
// A repository must check these requirements and the live matching session before
// spending a grant. An unsuitable grant remains available to another operation
// with the same binding and a less demanding policy.
type Requirement struct {
	SessionID          string
	UserID             string
	Purpose            string
	ContextDigest      string
	AuthenticatedAfter time.Time
	MinAssurance       session.AssuranceLevel
}

func (r Requirement) Validate() error {
	if r.SessionID == "" || r.UserID == "" || r.Purpose == "" || r.AuthenticatedAfter.IsZero() || assuranceRank(r.MinAssurance) == 0 {
		return sdk.ErrInvalidInput
	}
	return nil
}

// Accepts applies the same proof-age/assurance rules to a stored step-up grant
// and a session's primary authentication. Future-dated or absent proof is invalid.
func (r Requirement) Accepts(authenticatedAt time.Time, assurance session.AssuranceLevel, now time.Time) bool {
	return !authenticatedAt.IsZero() && !authenticatedAt.Before(r.AuthenticatedAfter) &&
		!authenticatedAt.After(now) && assuranceRank(r.MinAssurance) > 0 &&
		assuranceRank(assurance) >= assuranceRank(r.MinAssurance)
}

func assuranceRank(a session.AssuranceLevel) int {
	switch a {
	case session.AssuranceAAL1:
		return 1
	case session.AssuranceAAL2:
		return 2
	case session.AssuranceAAL3:
		return 3
	default:
		return 0
	}
}
