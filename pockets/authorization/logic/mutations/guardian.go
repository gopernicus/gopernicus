package mutations

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrInvalidGuardianPolicy = fmt.Errorf("authorization: invalid guardian policy: %w", sdk.ErrInvalidInput)

func validateGuardianPolicy(p GuardianPolicy) error {
	for _, r := range p.Rules {
		if r.MinAnchors < 0 {
			return ErrInvalidGuardianPolicy
		}
		if err := tuples.ValidateRefField("guardian relation", r.Relation); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidGuardianPolicy, err)
		}
		if r.ResourceType != "" {
			if err := tuples.ValidateRefField("guardian resource type", r.ResourceType); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidGuardianPolicy, err)
			}
		}
	}
	return nil
}

// A model-free guardian may protect any opaque label. An explicit subject-shape
// constraint must still permit a concrete anchor, otherwise the minimum is impossible.
func validateGuardianModel(p GuardianPolicy, engine *decisions.Service) error {
	if engine == nil || engine.CompiledModel() == nil {
		return nil
	}
	model := engine.CompiledModel().Snapshot()
	for _, r := range p.Rules {
		for _, rt := range model.ResourceTypes() {
			if r.ResourceType != "" && r.ResourceType != rt {
				continue
			}
			subjects := model.AllowedSubjects(rt, r.Relation)
			if len(subjects) == 0 {
				continue
			}
			concrete := false
			for _, s := range subjects {
				if s.Relation == "" {
					concrete = true
					break
				}
			}
			if !concrete {
				return fmt.Errorf("guardian %s.%s admits no concrete anchor: %w", rt, r.Relation, ErrInvalidGuardianPolicy)
			}
		}
	}
	return nil
}
