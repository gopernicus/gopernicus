package roles

import "context"

// ResolveScope checks an exact role assignment before a global fallback and
// reports the granting scope. The caller supplies a validated resource scope
// and an exact-read callback bound to one subject and role. A global query has
// no fallback; when both scopes grant, the exact assignment wins.
func ResolveScope(ctx context.Context, resourceType, resourceID string, exact func(context.Context, string, string) (bool, error)) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	held, err := exact(ctx, resourceType, resourceID)
	if err != nil {
		return false, "", err
	}
	if held {
		return true, ProvenanceDirect, nil
	}
	if resourceType != "" || resourceID != "" {
		if err := ctx.Err(); err != nil {
			return false, "", err
		}
		held, err = exact(ctx, "", "")
		if err != nil {
			return false, "", err
		}
		if held {
			return true, ProvenanceGlobal, nil
		}
	}
	return false, "", nil
}
