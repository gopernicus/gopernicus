package crud

import (
	"strings"
	"testing"
)

func TestNewOrder_ValidDirection(t *testing.T) {
	o := NewOrder("created_at", ASC)
	if o.Field != "created_at" || o.Direction != ASC {
		t.Errorf("NewOrder(ASC) = %+v, want {created_at ASC}", o)
	}

	o = NewOrder("updated_at", DESC)
	if o.Field != "updated_at" || o.Direction != DESC {
		t.Errorf("NewOrder(DESC) = %+v, want {updated_at DESC}", o)
	}
}

func TestNewOrder_InvalidDirectionDefaultsToASC(t *testing.T) {
	o := NewOrder("created_at", "INVALID")
	if o.Direction != ASC {
		t.Errorf("NewOrder(invalid) direction = %q, want %q", o.Direction, ASC)
	}
}

func TestParseOrder_EmptyReturnsDefault(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}}
	def := NewOrder("created_at", DESC)

	o, err := ParseOrder(mappings, "", def)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o != def {
		t.Errorf("ParseOrder(empty) = %+v, want %+v", o, def)
	}
}

func TestParseOrder_FieldOnly(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}, "name": {Column: "name"}}

	o, err := ParseOrder(mappings, "created_at", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" || o.Direction != ASC {
		t.Errorf("got %+v, want {created_at ASC}", o)
	}
}

func TestParseOrder_FieldWithDirection(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}}

	o, err := ParseOrder(mappings, "created_at:DESC", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" || o.Direction != DESC {
		t.Errorf("got %+v, want {created_at DESC}", o)
	}
}

func TestParseOrder_CaseInsensitiveDirection(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	def := NewOrder("id", ASC)

	for _, input := range []string{"name:desc", "name:Desc", "name:DESC"} {
		o, err := ParseOrder(mappings, input, def)
		if err != nil {
			t.Fatalf("ParseOrder(%q) err = %v", input, err)
		}
		if o.Direction != DESC {
			t.Errorf("ParseOrder(%q) direction = %q, want DESC", input, o.Direction)
		}
	}
}

func TestParseOrder_UnknownField(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	_, err := ParseOrder(mappings, "unknown_field", NewOrder("id", ASC))
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown order field") {
		t.Errorf("err = %q, want containing %q", err.Error(), "unknown order field")
	}
}

func TestParseOrder_UnknownDirection(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	_, err := ParseOrder(mappings, "name:SIDEWAYS", NewOrder("id", ASC))
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown direction") {
		t.Errorf("err = %q, want containing %q", err.Error(), "unknown direction")
	}
}

func TestParseOrder_TrimsWhitespace(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}

	o, err := ParseOrder(mappings, " name : desc ", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "name" || o.Direction != DESC {
		t.Errorf("got %+v, want {name DESC}", o)
	}
}

func TestParseOrder_FieldMapping(t *testing.T) {
	mappings := map[string]OrderField{"created": {Column: "created_at"}}

	o, err := ParseOrder(mappings, "created:asc", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" {
		t.Errorf("field = %q, want %q", o.Field, "created_at")
	}
}
