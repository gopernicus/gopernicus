package crud

// Field is one field of a sparse write: Set says whether the caller mentioned
// it at all, Value is what they sent. The zero value is ABSENT — "leave this
// column unchanged" — which is what makes it the PATCH representation: a domain
// update input holds a Field per patchable column, and Overlay folds the sparse
// input onto the current entity.
//
// # Nullable columns (normative)
//
// A nullable column rides Field[*T]; a NOT NULL column rides Field[T]. That
// resolves PATCH's three states without a second flag:
//
//	Field[*string]{}   // absent — leave the column as it is
//	Some[*string](nil) // explicit clear — write NULL
//	Some(&summary)     // set — write *summary
//
// Ruled once, here, so domains do not each pick a different convention.
//
// # How a handler builds one
//
// The strict body reader lives in sdk/foundation/web (web.ReadBody), not here:
// crud must never import net/http, because every store adapter imports crud and
// carries what it imports (guard G21). Its getters therefore return plain values
// plus presence, and the handler composes:
//
//	in := article.Patch{}
//	if body.Has("title") {
//	    in.Title = crud.Some(body.Str("title"))
//	}
//	if body.Has("summary") {
//	    in.Summary = crud.Some(body.OptStr("summary")) // *string: null clears
//	}
//	if err := body.Err(); err != nil {
//	    web.RespondJSONError(w, web.ErrValidation(err))
//	    return
//	}
//
// Field has no custom JSON marshalling on purpose: it is the domain's write
// representation, not a decode target.
type Field[T any] struct {
	Set   bool
	Value T
}

// Some returns a Field carrying v — the caller mentioned this field.
func Some[T any](v T) Field[T] {
	return Field[T]{Set: true, Value: v}
}

// Overlay folds a sparse field onto the current value: f.Value when the caller
// set it, current otherwise. It is the whole apply step of a PATCH.
//
//	a.Title = crud.Overlay(a.Title, in.Title)
func Overlay[T any](current T, f Field[T]) T {
	if f.Set {
		return f.Value
	}
	return current
}
