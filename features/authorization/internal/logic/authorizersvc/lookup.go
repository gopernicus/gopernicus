package authorizersvc

import "context"

// LookupResources returns all resource IDs of a type the subject can access with
// a permission (the prefilter pattern: look up authorized IDs, then pass them to
// the repository as WHERE id = ANY(@ids)).
//
// This is pure schema/tuple enumeration: IDs is ALWAYS a non-nil slice, and an
// empty slice means no access. A host that wants admin-sees-everything semantics
// checks for that in its own closure BEFORE calling here (and then skips ID
// filtering entirely) — the engine grants no bypass.
func (s *Service) LookupResources(ctx context.Context, subject Subject, permission, resourceType string) (LookupResult, error) {
	return s.lookupResourcesWithVisited(ctx, subject, permission, resourceType, make(map[string]bool))
}

func (s *Service) lookupResourcesWithVisited(ctx context.Context, subject Subject, permission, resourceType string, visited map[string]bool) (LookupResult, error) {
	// Cycle detection for non-self-referential Through chains.
	key := resourceType + ":" + permission
	if visited[key] {
		return LookupResult{IDs: []string{}}, nil
	}
	visited[key] = true

	rules := s.getPermissionRules(resourceType, permission)
	if len(rules.AnyOf) == 0 {
		return LookupResult{IDs: []string{}}, nil
	}

	seen := make(map[string]bool)
	var ids []string

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			throughResult, err := s.lookupThrough(ctx, subject, check, resourceType, visited)
			if err != nil {
				return LookupResult{}, err
			}
			for _, id := range throughResult.IDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		} else {
			directIDs, err := s.store.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, subject.Type, subject.ID)
			if err != nil {
				return LookupResult{}, err
			}
			for _, id := range directIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}

	if ids == nil {
		ids = []string{} // guarantee a non-nil slice — empty means no access
	}
	return LookupResult{IDs: ids}, nil
}

// lookupThrough resolves through-relations for LookupResources. A self-referential
// Through (target type == resource type, e.g. space→parent→space) is resolved via
// the store's recursive descendant walk instead of Go recursion, to walk
// arbitrarily deep trees safely in one query.
func (s *Service) lookupThrough(ctx context.Context, subject Subject, check PermissionCheck, resourceType string, visited map[string]bool) (LookupResult, error) {
	rtDef, ok := s.schema.ResourceTypes[resourceType]
	if !ok {
		return LookupResult{IDs: []string{}}, nil
	}
	relDef, ok := rtDef.Relations[check.Through]
	if !ok {
		return LookupResult{IDs: []string{}}, nil
	}

	seen := make(map[string]bool)
	var ids []string

	for _, ref := range relDef.AllowedSubjects {
		if ref.Type == resourceType {
			// Self-referential Through: find roots via direct-only rules, then
			// expand to descendants through the recursive store walk.
			rootIDs, err := s.lookupDirectOnly(ctx, subject, check.Permission, ref.Type)
			if err != nil {
				return LookupResult{}, err
			}
			if len(rootIDs) == 0 {
				continue
			}

			descendantIDs, err := s.store.LookupDescendantResourceIDs(ctx, resourceType, check.Through, ref.Type, rootIDs)
			if err != nil {
				return LookupResult{}, err
			}
			for _, id := range descendantIDs {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			continue
		}

		targetResult, err := s.lookupResourcesWithVisited(ctx, subject, check.Permission, ref.Type, visited)
		if err != nil {
			return LookupResult{}, err
		}
		if len(targetResult.IDs) == 0 {
			continue
		}

		throughIDs, err := s.store.LookupResourceIDsByRelationTarget(ctx, resourceType, check.Through, ref.Type, targetResult.IDs)
		if err != nil {
			return LookupResult{}, err
		}
		for _, id := range throughIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}

	if ids == nil {
		ids = []string{}
	}
	return LookupResult{IDs: ids}, nil
}

// lookupDirectOnly finds resource IDs where the subject has direct relations for
// a permission, skipping Through checks. Used to find root IDs for
// self-referential expansion without triggering recursion.
func (s *Service) lookupDirectOnly(ctx context.Context, subject Subject, permission, resourceType string) ([]string, error) {
	rules := s.getPermissionRules(resourceType, permission)
	if len(rules.AnyOf) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	var ids []string

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			continue
		}
		directIDs, err := s.store.LookupResourceIDs(ctx, resourceType, []string{check.Relation}, subject.Type, subject.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range directIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}

	return ids, nil
}
