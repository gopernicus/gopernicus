package decisions

import (
	"context"
	"errors"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrResourceNotApplicable makes a resource resolver's leaf false. It is
// interpreted only at that callback boundary; reader errors remain errors.
var ErrResourceNotApplicable = errors.New("authorization resource not applicable")

// ResourceSlot names a runtime input and its required resource type.
// Slots are permitted only in ad hoc expressions, never in compiled models.
type ResourceSlot struct{ Type, Key string }

// ResourceResolver resolves a reached input using the parent operation context.
// It must be read-only and respect cancellation. Its external reads are pinned
// inputs, not part of the authorization tuple snapshot.
type ResourceResolver func(context.Context, string) (authmodel.Resource, error)

// On binds one leaf to a fixed resource. Rebinding an explicit selector produces
// an invalid expression, detected before any I/O.
func On(resource authmodel.Resource, leaf Expression) Expression {
	if leaf.Resource != nil || leaf.ResourceSlot != nil || (leaf.CurrentResource && leaf.RoleName == "") {
		leaf.invalidSelector = true
		return leaf
	}
	leaf.CurrentResource = false
	leaf.Resource = &resource
	return leaf
}

// BindResource binds one leaf to a lazily resolved input. Reusing a key pins the
// same input across all leaves and any cache fallback within an operation.
func BindResource(resourceType, key string, leaf Expression) Expression {
	if leaf.Resource != nil || leaf.ResourceSlot != nil || (leaf.CurrentResource && leaf.RoleName == "") {
		leaf.invalidSelector = true
		return leaf
	}
	leaf.CurrentResource = false
	leaf.ResourceSlot = &ResourceSlot{Type: resourceType, Key: key}
	return leaf
}

func memoResourceResolver(parent context.Context, resolve ResourceResolver) func(context.Context, ResourceSlot) (authmodel.Resource, bool, error) {
	type result struct {
		resource authmodel.Resource
		err      error
	}
	memo := map[string]result{}
	return func(attempt context.Context, slot ResourceSlot) (authmodel.Resource, bool, error) {
		if err := parent.Err(); err != nil {
			return authmodel.Resource{}, false, err
		}
		if err := attempt.Err(); err != nil {
			return authmodel.Resource{}, false, err
		}
		value, ok := memo[slot.Key]
		if !ok {
			// A bounded cache attempt may expire while resolving. The input belongs to
			// the parent request and must remain usable by its durable fallback.
			value.resource, value.err = resolve(parent, slot.Key)
			if value.err == nil {
				value.err = tuples.On(value.resource.Type, value.resource.ID).Validate()
				if value.err == nil && value.resource.Type != slot.Type {
					value.err = fmt.Errorf("resource slot %q resolved type %q, want %q: %w", slot.Key, value.resource.Type, slot.Type, sdk.ErrInvalidInput)
				}
			}
			memo[slot.Key] = value
		}
		if err := parent.Err(); err != nil {
			return authmodel.Resource{}, false, err
		}
		if err := attempt.Err(); err != nil {
			return authmodel.Resource{}, false, err
		}
		if errors.Is(value.err, ErrResourceNotApplicable) {
			return authmodel.Resource{}, false, nil
		}
		return value.resource, value.err == nil, value.err
	}
}
