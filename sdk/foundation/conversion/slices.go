package conversion

// Overlap returns only the items from requested that also appear in allowed.
func Overlap[T comparable](requested, allowed []T) []T {
	set := make(map[T]struct{}, len(allowed))
	for _, v := range allowed {
		set[v] = struct{}{}
	}

	var result []T
	for _, v := range requested {
		if _, ok := set[v]; ok {
			result = append(result, v)
		}
	}
	return result
}
