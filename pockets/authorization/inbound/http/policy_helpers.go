package authorizationhttp

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrSelfTargetRefused is RefuseSelfTarget's refusal; it wraps sdk.ErrForbidden.
var ErrSelfTargetRefused = fmt.Errorf("authorization: a principal cannot assign a role to itself: %w", sdk.ErrForbidden)

// RefuseSelfTarget is a RoleWritePolicy refusing an assign whose subject is
// the acting principal (same Type and ID). Unassign passes, so the integrity
// policy, not this helper, decides whether a principal may drop its own role.
// It compares the direct subject only: a grant to a group the actor belongs to
// passes. Compose it with the host's own policy through AllOf.
func RefuseSelfTarget(_ context.Context, r RoleWriteRequest) error {
	if r.Operation == mutations.OpRoleAssign && r.Subject.Type == r.Principal.Type && r.Subject.ID == r.Principal.ID {
		return ErrSelfTargetRefused
	}
	return nil
}

// AllOf admits a role write only when every policy admits it, evaluated in
// order; the first refusal wins and later policies do not run. Zero policies or
// a nil entry is a wiring error and panics at construction.
func AllOf(policies ...RoleWritePolicy) RoleWritePolicy {
	if len(policies) == 0 {
		panic("authorizationhttp.AllOf: no policies")
	}
	for i, p := range policies {
		if p == nil {
			panic(fmt.Sprintf("authorizationhttp.AllOf: policy %d is nil", i))
		}
	}
	policies = append([]RoleWritePolicy(nil), policies...)
	return func(ctx context.Context, r RoleWriteRequest) error {
		for _, p := range policies {
			if err := p(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}
}
