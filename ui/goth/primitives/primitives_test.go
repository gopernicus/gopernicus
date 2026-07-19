package primitives

import (
	"errors"
	"testing"

	"github.com/a-h/templ"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		zero    bool
	}{
		{"empty is zero", "", false, true},
		{"relative path", "/articles/1", false, false},
		{"http", "http://example.com/x", false, false},
		{"https", "https://example.com/x", false, false},
		{"mailto", "mailto:a@example.com", false, false},
		{"tel", "tel:+15555555555", false, false},
		{"javascript rejected", "javascript:alert(1)", true, false},
		{"data rejected", "data:text/html,<script>", true, false},
		{"scheme-relative rejected", "//evil.com/x", true, false},
		{"control char rejected", "/a\x00b", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := ParseURL(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("ParseURL(%q) err = %v, want ErrInvalidURL", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseURL(%q): %v", tt.in, err)
			}
			if u.IsZero() != tt.zero {
				t.Errorf("ParseURL(%q).IsZero() = %v, want %v", tt.in, u.IsZero(), tt.zero)
			}
			if !tt.zero {
				if u.String() != tt.in {
					t.Errorf("String() = %q, want %q", u.String(), tt.in)
				}
				if string(u.SafeURL()) != tt.in {
					t.Errorf("SafeURL() = %q, want %q", u.SafeURL(), tt.in)
				}
			}
		})
	}
}

func TestMergeAttributesOwnedWins(t *testing.T) {
	caller := templ.Attributes{
		"id":         "caller-id",
		"role":       "caller-role",
		"data-extra": "keep",
	}
	owned := templ.Attributes{
		"role":      "owned-role",
		"data-slot": "trigger",
	}
	out := MergeAttributes(caller, owned)

	if out["role"] != "owned-role" {
		t.Errorf("role = %v, want owned-role (owned wins)", out["role"])
	}
	if out["id"] != "caller-id" {
		t.Errorf("id = %v, want caller-id (caller retained)", out["id"])
	}
	if out["data-extra"] != "keep" {
		t.Errorf("data-extra = %v, want keep", out["data-extra"])
	}
	if out["data-slot"] != "trigger" {
		t.Errorf("data-slot = %v, want trigger", out["data-slot"])
	}
}

func TestMergeAttributesDropsCallerClass(t *testing.T) {
	caller := templ.Attributes{"class": "sneaky", "id": "x"}
	out := MergeAttributes(caller, templ.Attributes{})
	if _, ok := out["class"]; ok {
		t.Errorf("caller class must be dropped; Base.Class is the only class channel")
	}
	if out["id"] != "x" {
		t.Errorf("non-class caller attrs must survive")
	}
}

func TestIDFactoryUniqueAndDeterministic(t *testing.T) {
	f := NewIDFactory()
	a := f.NextID("dialog")
	b := f.NextID("dialog")
	if a == b {
		t.Fatalf("IDFactory produced duplicate ids %q", a)
	}
	if a != "dialog-1" || b != "dialog-2" {
		t.Errorf("ids = %q,%q; want dialog-1,dialog-2", a, b)
	}
	// A fresh factory restarts numbering (request-scoped, no global counter).
	if got := NewIDFactory().NextID("dialog"); got != "dialog-1" {
		t.Errorf("fresh factory NextID = %q, want dialog-1", got)
	}
}
