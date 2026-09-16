// Package tuplekey owns the private versioned, byte-ordered canonical cursor key.
package tuplekey

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const Version = "2"

// DecodeCursor validates the listing envelope as well as its versioned key.
// Unlike a general list cursor, a tuple cursor may not change its order field
// or carry a different primary key: both coordinates are the same full fact.
func DecodeCursor(token string) (*tuples.Tuple, error) {
	if token == "" {
		return nil, nil
	}
	cursor, err := list.DecodeCursor(token, "tuple_key")
	if err != nil {
		return nil, err
	}
	if cursor == nil {
		return nil, fmt.Errorf("tuple cursor requires tuple_key order: %w", sdk.ErrInvalidInput)
	}
	key, ok := cursor.OrderValue.(string)
	if !ok || key != cursor.PK {
		return nil, fmt.Errorf("tuple cursor order and primary key disagree: %w", sdk.ErrInvalidInput)
	}
	fact, err := Decode(key)
	if err != nil {
		return nil, err
	}
	return &fact, nil
}

// Encode requires a validated tuple. Validation forbids the field separator.
func Encode(t tuples.Tuple) string {
	kind := "1"
	if t.Scope.Kind == tuples.ResourceScope {
		kind = "2"
	}
	return strings.Join([]string{Version, kind, t.Scope.Type, t.Scope.ID, t.Relation, t.Subject.Type, t.Subject.ID, t.Subject.Relation}, "\x01")
}

func Decode(key string) (tuples.Tuple, error) {
	p := strings.Split(key, "\x01")
	if len(p) != 8 || p[0] != Version || (p[1] != "1" && p[1] != "2") {
		return tuples.Tuple{}, fmt.Errorf("invalid or obsolete tuple cursor: %w", sdk.ErrInvalidInput)
	}
	kind := tuples.GlobalScope
	if p[1] == "2" {
		kind = tuples.ResourceScope
	}
	t := tuples.Tuple{Scope: tuples.Scope{Kind: kind, Type: p[2], ID: p[3]}, Relation: p[4], Subject: tuples.SubjectRef{Type: p[5], ID: p[6], Relation: p[7]}}
	if err := t.Validate(); err != nil {
		return tuples.Tuple{}, err
	}
	return t, nil
}
