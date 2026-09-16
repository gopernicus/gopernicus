package documents

import (
	"context"
	"fmt"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

type SQLListing struct {
	reader Reader
	codec  *CursorCodec
	bypass Bypass
}

// NewSQLListing supports exactly document:view = Direct(viewer), where viewer
// allows concrete users only. Other permissions/types may coexist. Inheritance,
// usersets, additional OR branches and role-owned view are rejected at construction.
// The host wires a business reader with same-database exact membership support.
// An unconfigured reader rejects membership restrictions before issuing SQL;
// this adapter never discovers, synchronizes or copies another store's grants.
func NewSQLListing(reader Reader, authorizer *decisions.Service, codec *CursorCodec, bypass Bypass) (*SQLListing, error) {
	if reader == nil || authorizer == nil || codec == nil {
		return nil, fmt.Errorf("documents: incomplete SQL listing: %w", sdk.ErrInvalidInput)
	}
	schema := authorizer.GetSchema()
	checks := schema.Checks("document", "view")
	subjects := schema.AllowedSubjects("document", "viewer")
	if len(checks) != 1 || checks[0].Relation != "viewer" || checks[0].Through != "" || checks[0].Permission != "" ||
		len(subjects) != 1 || subjects[0].Type != "user" || subjects[0].Relation != "" {
		return nil, fmt.Errorf("documents: SQL listing requires concrete-user Direct(viewer) policy: %w", sdk.ErrInvalidInput)
	}
	return &SQLListing{reader: reader, codec: codec, bypass: bypass}, nil
}

func (l *SQLListing) ListVisible(ctx context.Context, principal sdk.Principal, query Query) (Page, error) {
	if err := query.normalize(); err != nil {
		return Page{}, err
	}
	if err := model.PrincipalFrom(principal).Validate(); err != nil {
		return Page{}, err
	}
	binding := cursorBinding(principal, query)
	after, err := l.codec.decode(query.Cursor, binding)
	if err != nil {
		return Page{}, err
	}
	unrestricted, err := checkBypass(ctx, l.bypass, principal)
	if err != nil {
		return Page{}, err
	}
	restriction := domain.Restriction{Unrestricted: unrestricted}
	if !unrestricted && principal.Type == "user" {
		restriction.Membership = &domain.ExactMembership{SubjectType: principal.Type, SubjectID: principal.ID, Relation: "viewer"}
	}
	rows, more, err := l.reader.Read(ctx, query.businessQuery(), after, query.Limit, restriction)
	if err != nil {
		return Page{}, err
	}
	return pageFromRows(l.codec, binding, rows, more)
}
