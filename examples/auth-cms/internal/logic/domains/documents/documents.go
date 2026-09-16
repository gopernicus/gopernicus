package documents

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

type Document struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
}

// Validate bounds persisted sort values as well as ordinary document input.
// Even JSON escaping the maximum name/ID fits the 4096-byte encrypted cursor.
func (d Document) Validate() error {
	if !validText(d.ID, 128) || !validText(d.TenantID, 128) || !validText(d.Name, 512) ||
		strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.TenantID) == "" || strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("documents: invalid ID, tenant or name: %w", sdk.ErrInvalidInput)
	}
	return nil
}

func validText(value string, maxBytes int) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type Query struct {
	TenantID string
	Search   string
	Desc     bool
	Limit    int
}

// Position is the persisted bytewise name/ID ordering used by storage.
type Position struct {
	NameKey string `json:"name_key"`
	ID      string `json:"id"`
}

type Row struct {
	ID       string
	TenantID string
	Name     string
	NameKey  string
}

func (r Row) Document() Document {
	return Document{ID: r.ID, TenantID: r.TenantID, Name: r.Name}
}

func (r Row) Position() Position { return Position{NameKey: r.NameKey, ID: r.ID} }

// Restriction is data selected by the caller. Its zero value matches no rows.
// Exactly one of the unrestricted, ID-set or exact-membership forms may be used.
type Restriction struct {
	Unrestricted bool
	IDs          []string
	Membership   *ExactMembership
}

type ExactMembership struct {
	SubjectType string
	SubjectID   string
	Relation    string
}

func (r Restriction) Validate() error {
	if (r.Unrestricted && (len(r.IDs) > 0 || r.Membership != nil)) || (len(r.IDs) > 0 && r.Membership != nil) {
		return fmt.Errorf("documents: mixed restrictions: %w", sdk.ErrInvalidInput)
	}
	for _, id := range r.IDs {
		if !validText(id, 128) || strings.TrimSpace(id) == "" {
			return fmt.Errorf("documents: invalid restricted ID: %w", sdk.ErrInvalidInput)
		}
	}
	if m := r.Membership; m != nil {
		for _, field := range []string{m.SubjectType, m.SubjectID, m.Relation} {
			if !validText(field, 256) || strings.TrimSpace(field) == "" {
				return fmt.Errorf("documents: invalid membership restriction: %w", sdk.ErrInvalidInput)
			}
		}
	}
	return nil
}

// Reader applies restrictions and tenant/search predicates before ordering and
// pagination. It returns persisted sort values, without evaluating host policy.
type Reader interface {
	Read(context.Context, Query, Position, int, Restriction) ([]Row, bool, error)
}

type Service struct{ reader Reader }

func New(reader Reader) (*Service, error) {
	if reader == nil {
		return nil, fmt.Errorf("documents: reader is required: %w", sdk.ErrInvalidInput)
	}
	return &Service{reader: reader}, nil
}

func (s *Service) Read(ctx context.Context, query Query, after Position, limit int, restriction Restriction) ([]Row, bool, error) {
	if err := query.Normalize(); err != nil {
		return nil, false, err
	}
	if err := restriction.Validate(); err != nil {
		return nil, false, err
	}
	// Unicode lowercase can expand a name's UTF-8 bytes. Bound the persisted
	// key by the largest encoding per input byte, not the original name bound.
	if limit < 1 || limit == math.MaxInt || !validText(after.NameKey, 512*utf8.UTFMax) || !validText(after.ID, 128) || (after.ID == "" && after.NameKey != "") {
		return nil, false, fmt.Errorf("documents: invalid read bounds: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !restriction.Unrestricted && len(restriction.IDs) == 0 && restriction.Membership == nil {
		return []Row{}, false, nil
	}
	return s.reader.Read(ctx, query, after, limit, restriction)
}

// Normalize defines the host query independently from the permission strategy.
func (q *Query) Normalize() error {
	if !validText(q.TenantID, 128) || !validText(q.Search, 200) {
		return fmt.Errorf("documents: invalid tenant or search: %w", sdk.ErrInvalidInput)
	}
	q.TenantID = strings.TrimSpace(q.TenantID)
	q.Search = strings.ToLower(strings.TrimSpace(q.Search))
	if q.TenantID == "" {
		return fmt.Errorf("documents: invalid tenant or search: %w", sdk.ErrInvalidInput)
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 {
		return fmt.Errorf("documents: limit must be between 1 and 100: %w", sdk.ErrInvalidInput)
	}
	return nil
}
