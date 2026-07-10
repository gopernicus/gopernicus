package conversion

// Ptr returns a pointer to the given value.
// Replaces all type-specific pointer helpers (StringPtr, IntPtr, BoolPtr, TimePtr, etc.).
func Ptr[T any](v T) *T {
	return &v
}

// Deref safely dereferences a pointer, returning the zero value of T if nil.
// Replaces StringOrEmpty, IntOrZero, BoolOrFalse, TimeOrEmpty, etc.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// DerefOr safely dereferences a pointer, returning the fallback if nil.
// Replaces StringOrDefault, StringPtrValue, etc.
func DerefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
