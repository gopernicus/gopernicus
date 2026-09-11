package decisions

import "context"

type exactRoleReader func(context.Context, string, string, string, string, string) (bool, error)

type roleReadKey struct {
	subjectType, subjectID, roleName, resourceType, resourceID string
}

// memoRoleReads retains successful exact facts for one sequential CheckBatch.
// The closure never escapes the call, and it does not wrap guarded dependency
// readers. Cache hits still honor request cancellation.
func memoRoleReads(read exactRoleReader) exactRoleReader {
	cache := make(map[roleReadKey]bool)
	return func(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		key := roleReadKey{subjectType, subjectID, roleName, resourceType, resourceID}
		if held, ok := cache[key]; ok {
			return held, nil
		}
		held, err := read(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
		if err != nil {
			return false, err
		}
		cache[key] = held
		return held, nil
	}
}
