package sdk

// Deref returns the pointed-to value, or the zero value of T if p is nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DerefOr returns the pointed-to value, or fallback if p is nil.
// A non-nil pointer to a zero value retains that value.
func DerefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
