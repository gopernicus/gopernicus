package roles

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// TupleValidator validates explicit relation and subject-shape constraints.
type TupleValidator interface{ ValidateTuple(tuples.Tuple) error }

// Writer holds trusted raw membership changes; request-facing writes use guards.
type Writer struct {
	store     tuples.Storer
	validator TupleValidator
}
type WriterOption func(*Writer)

// WithValidator enforces explicit subject-shape constraints on additions.
func WithValidator(v TupleValidator) WriterOption { return func(w *Writer) { w.validator = v } }

func NewWriter(store tuples.Storer, opts ...WriterOption) (*Writer, error) {
	if isNil(store) {
		return nil, fmt.Errorf("authorization: tuple store is required: %w", sdk.ErrInvalidInput)
	}
	w := &Writer{store: store}
	for _, opt := range opts {
		if opt == nil {
			return nil, sdk.ErrInvalidInput
		}
		opt(w)
	}
	if w.validator != nil && isNil(w.validator) {
		return nil, sdk.ErrInvalidInput
	}
	return w, nil
}
func isNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func (w *Writer) AssignRole(ctx context.Context, a Assignment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if w.validator != nil {
		if err := w.validator.ValidateTuple(a.Tuple()); err != nil {
			return err
		}
	}
	return w.store.ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{a.Tuple()}})
}
func (w *Writer) UnassignRole(ctx context.Context, a Assignment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return w.store.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{a.Tuple()}})
}
