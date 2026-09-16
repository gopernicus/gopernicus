package tuples

import (
	"encoding/json"
	"fmt"
)

const (
	GlobalScope ScopeKind = iota + 1
	ResourceScope
)

// ScopeKind distinguishes global facts from facts about a specific resource.
type ScopeKind uint8

// Scope names either global scope or an exact resource. The zero value is invalid.
type Scope struct {
	Kind ScopeKind `json:"kind"`
	Type string    `json:"resource_type,omitempty"`
	ID   string    `json:"resource_id,omitempty"`
}

func (k ScopeKind) MarshalJSON() ([]byte, error) {
	switch k {
	case GlobalScope:
		return []byte(`"global"`), nil
	case ResourceScope:
		return []byte(`"resource"`), nil
	default:
		return nil, fmt.Errorf("invalid scope kind: %w", ErrInvalidRef)
	}
}
func (k *ScopeKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("scope kind must be global or resource: %w", ErrInvalidRef)
	}
	switch name {
	case "global":
		*k = GlobalScope
	case "resource":
		*k = ResourceScope
	default:
		return fmt.Errorf("unknown scope kind %q: %w", name, ErrInvalidRef)
	}
	return nil
}

// Global returns the explicit global scope, with no resource coordinates.
func Global() Scope { return Scope{Kind: GlobalScope} }

// On returns a resource scope without changing its coordinates. Empty
// coordinates are invalid; they never turn a resource scope into global scope.
func On(resourceType, resourceID string) Scope {
	return Scope{Kind: ResourceScope, Type: resourceType, ID: resourceID}
}

// Validate checks the scope tag and its resource coordinates.
func (s Scope) Validate() error {
	switch s.Kind {
	case GlobalScope:
		if s.Type != "" || s.ID != "" {
			return fmt.Errorf("global scope must not have resource coordinates: %w", ErrInvalidRef)
		}
		return nil
	case ResourceScope:
		if err := ValidateRefField("resource type", s.Type); err != nil {
			return err
		}
		return ValidateRefField("resource id", s.ID)
	default:
		return fmt.Errorf("unknown scope kind %d: %w", s.Kind, ErrInvalidRef)
	}
}
