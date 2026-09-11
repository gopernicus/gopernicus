package mutations

import (
	"fmt"
	"slices"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrInvalidGuardianPolicy reports invariant configuration the host model cannot satisfy.
var ErrInvalidGuardianPolicy = fmt.Errorf("authorization: invalid guardian policy: %w", sdk.ErrInvalidInput)

func validateGuardianPolicy(schema relationships.SchemaSnapshot, policy GuardianPolicy) error {
	for _, rule := range policy.Rules {
		if err := authmodel.ValidateRefField("guardian relation", rule.Relation); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidGuardianPolicy, err)
		}
		if rule.MinAnchors < 0 {
			return fmt.Errorf("%w: minimum for %q must not be negative", ErrInvalidGuardianPolicy, rule.Relation)
		}
		var types []string
		if rule.ResourceType == "" {
			types = schema.ResourceTypes()
			slices.Sort(types)
			if len(types) == 0 {
				return fmt.Errorf("%w: %q requires a relationship model", ErrInvalidGuardianPolicy, rule.Relation)
			}
		} else {
			if err := authmodel.ValidateRefField("guardian resource type", rule.ResourceType); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidGuardianPolicy, err)
			}
			types = []string{rule.ResourceType}
		}
		for _, resourceType := range types {
			if !slices.Contains(schema.ResourceTypes(), resourceType) {
				return fmt.Errorf("%w: unknown resource type %q", ErrInvalidGuardianPolicy, resourceType)
			}
			if !slices.Contains(schema.Relations(resourceType), rule.Relation) {
				return fmt.Errorf("%w: %s has no relation %q", ErrInvalidGuardianPolicy, resourceType, rule.Relation)
			}
			concrete := false
			for _, subject := range schema.AllowedSubjects(resourceType, rule.Relation) {
				if subject.Relation == "" {
					concrete = true
					break
				}
			}
			if !concrete {
				return fmt.Errorf("%w: %s.%s permits no concrete guardian", ErrInvalidGuardianPolicy, resourceType, rule.Relation)
			}
		}
	}
	return nil
}
