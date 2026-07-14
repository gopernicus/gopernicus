package command

import (
	"errors"
	"testing"
)

// Validate accepts the two well-formed stages and rejects every malformed shape
// with the corresponding static sentinel — the parsing-rejection contract Open
// relies on.
func TestValidate(t *testing.T) {
	rendered := Envelope{
		Version: Version1, Kind: "email", Purpose: "login_code", Key: "k",
		Stage: StageRendered, Destination: "user@example.com", Body: "b",
	}
	opaque := Envelope{
		Version: Version1, Kind: "phone", Purpose: "login_code", Key: "k",
		Stage: StageOpaque, ResolutionInput: "+15550001111",
	}
	cases := []struct {
		name string
		env  Envelope
		want error
	}{
		{"valid rendered", rendered, nil},
		{"valid opaque", opaque, nil},
		{"rendered html only", func() Envelope { e := rendered; e.Body = ""; e.HTML = "<p>x</p>"; return e }(), nil},
		{"zero version", func() Envelope { e := rendered; e.Version = 0; return e }(), ErrUnknownVersion},
		{"future version", func() Envelope { e := rendered; e.Version = Version1 + 1; return e }(), ErrUnknownVersion},
		{"missing kind", func() Envelope { e := rendered; e.Kind = ""; return e }(), ErrMissingKind},
		{"missing purpose", func() Envelope { e := rendered; e.Purpose = ""; return e }(), ErrMissingPurpose},
		{"missing key", func() Envelope { e := rendered; e.Key = ""; return e }(), ErrMissingKey},
		{"empty stage", func() Envelope { e := rendered; e.Stage = ""; return e }(), ErrInvalidStage},
		{"unknown stage", func() Envelope { e := rendered; e.Stage = "half"; return e }(), ErrInvalidStage},
		{"opaque without resolution input", func() Envelope { e := opaque; e.ResolutionInput = ""; return e }(), ErrStageMismatch},
		{"opaque with body", func() Envelope { e := opaque; e.Body = "x"; return e }(), ErrStageMismatch},
		{"opaque with secret", func() Envelope { e := opaque; e.Secret = "x"; return e }(), ErrStageMismatch},
		{"opaque with destination", func() Envelope { e := opaque; e.Destination = "x"; return e }(), ErrStageMismatch},
		{"rendered without destination", func() Envelope { e := rendered; e.Destination = ""; return e }(), ErrStageMismatch},
		{"rendered without content", func() Envelope { e := rendered; e.Body = ""; e.HTML = ""; return e }(), ErrStageMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.env.Validate()
			if c.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Validate() = %v, want %v", err, c.want)
			}
		})
	}
}

// The constructors set the current version and stage and reject invalid input.
func TestConstructors(t *testing.T) {
	if _, err := NewOpaque("phone", "login_code", "k", "+1"); err != nil {
		t.Fatalf("NewOpaque valid: %v", err)
	}
	if _, err := NewOpaque("phone", "", "k", "+1"); !errors.Is(err, ErrMissingPurpose) {
		t.Fatalf("NewOpaque(missing purpose) = %v, want ErrMissingPurpose", err)
	}
	if _, err := NewRendered("email", "login_code", "k", "u@x", "s", "b", "", ""); err != nil {
		t.Fatalf("NewRendered valid: %v", err)
	}
	if _, err := NewRendered("email", "login_code", "k", "", "s", "b", "", ""); !errors.Is(err, ErrStageMismatch) {
		t.Fatalf("NewRendered(no destination) = %v, want ErrStageMismatch", err)
	}
	env, _ := NewOpaque("phone", "login_code", "k", "+1")
	if env.Version != Version1 || !env.Opaque() {
		t.Fatalf("NewOpaque produced %+v, want version %d opaque", env, Version1)
	}
}
