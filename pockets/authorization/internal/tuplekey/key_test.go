package tuplekey

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestRoundTripAndVersionFence(t *testing.T) {
	max := strings.Repeat("é", 128)
	for _, v := range []tuples.Tuple{
		{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "u"}},
		{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}},
		{Scope: tuples.On(max, max), Relation: max, Subject: tuples.SubjectRef{Type: max, ID: max, Relation: max}},
	} {
		key := Encode(v)
		got, err := Decode(key)
		if err != nil || got != v {
			t.Fatalf("round trip: %+v %v", got, err)
		}
		for _, bad := range []string{"", strings.Replace(key, "2\x01", "1\x01", 1), key + "\x01extra", strings.TrimPrefix(key, "2\x01"), "2\x013\x01\x01\x01admin\x01user\x01u\x01"} {
			if _, err := Decode(bad); err == nil {
				t.Fatalf("accepted obsolete/malformed cursor %q", bad)
			}
		}
	}
}

func TestListingCursorEnvelopeAndWireCompatibility(t *testing.T) {
	fact := tuples.Tuple{Scope: tuples.On("doc", "d"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}}
	key := "2\x012\x01doc\x01d\x01viewer\x01group\x01g\x01member"
	if Encode(fact) != key {
		t.Fatal("version-2 key bytes changed")
	}
	for _, c := range []struct {
		field string
		value any
		pk    string
		valid bool
	}{
		{"tuple_key", key, key, true},
		{"role_key", key, key, false},
		{"tuple_key", key, key + "other", false},
		{"tuple_key", 1, key, false},
		{"tuple_key", strings.Replace(key, "2\x01", "1\x01", 1), strings.Replace(key, "2\x01", "1\x01", 1), false},
	} {
		token, err := list.EncodeCursor(c.field, c.value, c.pk)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCursor(token)
		if c.valid {
			if err != nil || got == nil || *got != fact {
				t.Fatalf("valid cursor: %+v/%v", got, err)
			}
		} else if !errors.Is(err, sdk.ErrInvalidInput) || got != nil {
			t.Fatalf("invalid cursor accepted: %+v/%v", got, err)
		}
	}
	if got, err := DecodeCursor(""); got != nil || err != nil {
		t.Fatalf("empty continuation: %+v/%v", got, err)
	}
}
func TestFullIdentityByteOrder(t *testing.T) {
	base := tuples.Tuple{Scope: tuples.On("doc", "a"), Relation: "member", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}
	variants := []tuples.Tuple{base, base, base, base}
	variants[0].Scope = tuples.Global()
	variants[2].Subject.Relation = "member"
	variants[3].Scope.ID = "é"
	previous := ""
	for _, v := range variants {
		k := Encode(v)
		if k <= previous {
			t.Fatalf("order %q <= %q", k, previous)
		}
		previous = k
	}
	invalid := base
	invalid.Subject.ID = "bad\x01id"
	if _, err := Decode(Encode(invalid)); err == nil {
		t.Fatal("separator accepted")
	}
}

func FuzzDecodeCanonicalKey(f *testing.F) {
	f.Add(Encode(tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "u"}}))
	f.Add(Encode(tuples.Tuple{Scope: tuples.On("doc", "é"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}}))
	f.Add("1\x01obsolete")
	f.Fuzz(func(t *testing.T, key string) {
		fact, err := Decode(key)
		if err != nil {
			return
		}
		if err := fact.Validate(); err != nil {
			t.Fatalf("accepted invalid fact: %v", err)
		}
		if Encode(fact) != key {
			t.Fatal("accepted noncanonical or ambiguous key")
		}
	})
}
