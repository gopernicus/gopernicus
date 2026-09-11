package roles

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/sdk"
)

// Writer performs trusted raw role assignments against a caller-owned repository.
// It applies structural validation, without a permission model or actor guard.
// Ordinary actor writes use mutations.Service instead.
type Writer struct{ store Storer }

func NewWriter(store Storer) (*Writer, error) {
	if isNil(store) {
		return nil, fmt.Errorf("authorization: roles store is required: %w", sdk.ErrInvalidInput)
	}
	return &Writer{store: store}, nil
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
func (s *Writer) AssignRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return err
	}
	return s.store.Assign(ctx, Assignment{
		SubjectType:  subjectType,
		SubjectID:    subjectID,
		Role:         roleName,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
}

func (s *Writer) UnassignRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := validateAssignment(subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return err
	}
	return s.store.Unassign(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
}
