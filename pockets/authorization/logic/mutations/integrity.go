package mutations

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrInvalidIntegrityPolicy = fmt.Errorf("authorization: invalid integrity policy: %w", sdk.ErrInvalidInput)

func validateIntegrityPolicy(p IntegrityPolicy) error {
	for _, r := range p.Rules {
		if r.MinSubjects < 0 {
			return ErrInvalidIntegrityPolicy
		}
		if err := tuples.ValidateRefField("integrity relation", r.Relation); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidIntegrityPolicy, err)
		}
		if r.ResourceType != "" {
			if err := tuples.ValidateRefField("integrity resource type", r.ResourceType); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidIntegrityPolicy, err)
			}
		}
	}
	return nil
}

// A model-free integrity may protect any opaque label. An explicit subject-shape
// constraint must still permit a concrete subject, otherwise the minimum is impossible.
func validateIntegrityModel(p IntegrityPolicy, engine *decisions.CompiledModel) error {
	if engine == nil {
		return nil
	}
	model := engine.Snapshot()
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
				return fmt.Errorf("integrity %s.%s admits no concrete subject: %w", rt, r.Relation, ErrInvalidIntegrityPolicy)
			}
		}
	}
	return nil
}
