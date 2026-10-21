package authorization

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Reference kinds accepted by ValidatePortableRef.
const (
	// KindType names a resource, principal or subject type.
	KindType = "type"
	// KindRelation names a relation or a permission.
	KindRelation = "relation"
	// KindID names a resource or principal ID.
	KindID = "id"
)

const (
	minTypeBytes     = 3
	maxTypeBytes     = 64
	maxRelationBytes = 50
	maxIDBytes       = 256
	maxObjectBytes   = 256
)

// Validate checks structure only: the principal, the permission and the
// resource type and ID are present. Identifier grammar belongs to each
// implementation; ValidatePortable is the opt-in strict check.
func (r CheckRequest) Validate() error {
	if err := validatePrincipal(r.Principal); err != nil {
		return err
	}
	if err := required("permission", r.Permission); err != nil {
		return err
	}
	if err := required("resource type", r.Resource.Type); err != nil {
		return err
	}
	return required("resource id", r.Resource.ID)
}

// Validate checks structure only: the principal, the permission and the
// resource type are present and Limit is not negative. The upper bound and
// After are checked by the implementation, which knows its Limits and its
// cursors.
func (r LookupRequest) Validate() error {
	if r.Limit < 0 {
		return fmt.Errorf("authorization: lookup limit must not be negative, got %d: %w", r.Limit, sdk.ErrInvalidInput)
	}
	if err := validatePrincipal(r.Principal); err != nil {
		return err
	}
	if err := required("permission", r.Permission); err != nil {
		return err
	}
	return required("resource type", r.ResourceType)
}

// ValidatePortable applies the strict identifier grammar every known
// implementation can represent (ValidatePortableObject for the principal and
// the resource, ValidatePortableRef for the permission). It is opt-in: no
// implementation is required to call it. A host runs it in tests, or before
// moving to a vendor, to prove its identifiers travel.
func ValidatePortable(r CheckRequest) error {
	if err := ValidatePortableObject(r.Principal.Type, r.Principal.ID); err != nil {
		return err
	}
	if err := ValidatePortableRef(KindRelation, r.Permission); err != nil {
		return err
	}
	return ValidatePortableObject(r.Resource.Type, r.Resource.ID)
}

// ValidatePortableRef enforces the portable identifier grammar. Types match
// ^[a-z][a-z0-9_]{1,62}[a-z0-9]$; relations and permissions match the same
// pattern in at most 50 bytes; IDs match ^[a-zA-Z0-9/_|\-=+]+$ in at most 256
// bytes, so "*" is never nameable. A violation wraps sdk.ErrInvalidInput.
func ValidatePortableRef(kind, value string) error {
	switch kind {
	case KindType:
		if !validName(value, maxTypeBytes) {
			return fmt.Errorf("authorization: type %.64q violates ^[a-z][a-z0-9_]{1,62}[a-z0-9]$: %w", value, sdk.ErrInvalidInput)
		}
	case KindRelation:
		if !validName(value, maxRelationBytes) {
			return fmt.Errorf("authorization: relation %.64q violates ^[a-z][a-z0-9_]{1,62}[a-z0-9]$ in at most %d bytes: %w", value, maxRelationBytes, sdk.ErrInvalidInput)
		}
	case KindID:
		if !validID(value) {
			return fmt.Errorf("authorization: id %.64q violates ^[a-zA-Z0-9/_|\\-=+]+$ in at most %d bytes: %w", value, maxIDBytes, sdk.ErrInvalidInput)
		}
	default:
		return fmt.Errorf("authorization: unknown reference kind %q: %w", kind, sdk.ErrInvalidInput)
	}
	return nil
}

// ValidatePortableObject validates a type and an ID together: both pass
// ValidatePortableRef and len(objectType)+1+len(id) <= 256.
func ValidatePortableObject(objectType, id string) error {
	if err := ValidatePortableRef(KindType, objectType); err != nil {
		return err
	}
	if err := ValidatePortableRef(KindID, id); err != nil {
		return err
	}
	if len(objectType)+1+len(id) > maxObjectBytes {
		return fmt.Errorf("authorization: object %s:<%d bytes> exceeds %d bytes: %w", objectType, len(id), maxObjectBytes, sdk.ErrInvalidInput)
	}
	return nil
}

func validatePrincipal(p Principal) error {
	if err := required("principal type", p.Type); err != nil {
		return err
	}
	return required("principal id", p.ID)
}

func required(field, value string) error {
	if value == "" {
		return fmt.Errorf("authorization: %s must not be empty: %w", field, sdk.ErrInvalidInput)
	}
	return nil
}

func validName(value string, maxBytes int) bool {
	if len(value) < minTypeBytes || len(value) > maxBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		lower := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		switch i {
		case 0:
			if !lower {
				return false
			}
		case len(value) - 1:
			if !lower && !digit {
				return false
			}
		default:
			if !lower && !digit && c != '_' {
				return false
			}
		}
	}
	return true
}

func validID(value string) bool {
	if value == "" || len(value) > maxIDBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/', c == '_', c == '|', c == '-', c == '=', c == '+':
		default:
			return false
		}
	}
	return true
}
