package relationships

import (
	"slices"
	"sort"
)

// distinctSorted folds a caller's id list to the distinct ids in byte order —
// the shape a set read takes and the shape the walk dedups on.
func distinctSorted(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	sort.Strings(out)
	return slices.Compact(out)
}
