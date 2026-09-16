package tuples

// SubjectRef names a concrete subject when Relation is empty, or the exact
// userset Type:ID#Relation otherwise. All fields are opaque exact strings.
type SubjectRef struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Relation string `json:"relation,omitempty"`
}

// IsUserset reports whether the reference names a userset.
func (s SubjectRef) IsUserset() bool { return s.Relation != "" }

// String renders Type:ID(#Relation) for logs and debugging. Opaque fields may
// contain these separators, so this is not a serialization or identity key.
func (s SubjectRef) String() string {
	if s.Relation == "" {
		return s.Type + ":" + s.ID
	}
	return s.Type + ":" + s.ID + "#" + s.Relation
}

// Validate checks the required type/ID and the optional userset relation.
// It applies no schema or traversal rules.
func (s SubjectRef) Validate() error {
	if err := ValidateRefField("subject type", s.Type); err != nil {
		return err
	}
	if err := ValidateRefField("subject id", s.ID); err != nil {
		return err
	}
	if s.Relation != "" {
		return ValidateRefField("subject relation", s.Relation)
	}
	return nil
}
