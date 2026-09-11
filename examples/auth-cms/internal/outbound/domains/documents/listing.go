package documents

import (
	"context"
	"fmt"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

type Strategy string

const (
	CompleteSet Strategy = "complete_set"
	Candidates  Strategy = "candidates"
)

type row struct {
	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
	Name     string `db:"name"`
	NameKey  string `db:"name_key"`
}

func (r row) document() domain.Document {
	return domain.Document{ID: r.ID, TenantID: r.TenantID, Name: r.Name}
}

// Reader applies the complete filter BEFORE business ordering and pagination.
// It may use a different datastore from authorization. Rows include the actual
// persisted sort key so the cursor and the storage comparison use identical data.
type Reader interface {
	read(context.Context, domain.Query, position, int, decisions.ResourceSet) ([]row, bool, error)
}

type Bypass func(context.Context, sdk.Principal) (bool, error)

type Listing struct {
	reader     Reader
	authorizer *decisions.Service
	codec      *CursorCodec
	strategy   Strategy
	batchSize  int
	bypass     Bypass
}

func NewListing(reader Reader, authorizer *decisions.Service, codec *CursorCodec, strategy Strategy, batchSize int, bypass Bypass) (*Listing, error) {
	if reader == nil || authorizer == nil || codec == nil || batchSize < 0 || (strategy != CompleteSet && strategy != Candidates) {
		return nil, fmt.Errorf("documents: invalid listing configuration: %w", sdk.ErrInvalidInput)
	}
	return &Listing{reader: reader, authorizer: authorizer, codec: codec, strategy: strategy, batchSize: batchSize, bypass: bypass}, nil
}

func (l *Listing) ListVisible(ctx context.Context, principal sdk.Principal, query domain.Query) (domain.Page, error) {
	if err := query.Normalize(); err != nil {
		return domain.Page{}, err
	}
	if err := model.PrincipalFrom(principal).Validate(); err != nil {
		return domain.Page{}, err
	}
	binding := cursorBinding(principal, query)
	after, err := l.codec.decode(query.Cursor, binding)
	if err != nil {
		return domain.Page{}, err
	}
	unrestricted, err := checkBypass(ctx, l.bypass, principal)
	if err != nil {
		return domain.Page{}, err
	}
	if unrestricted || l.strategy == CompleteSet {
		filter := decisions.ResourceSet{Unrestricted: unrestricted}
		if !unrestricted {
			filter, err = l.authorizer.LookupAllResourceIDs(ctx, model.PrincipalFrom(principal), "view", "document")
			if err != nil {
				return domain.Page{}, err
			}
		}
		rows, more, err := l.reader.read(ctx, query, after, query.Limit, filter)
		if err != nil {
			return domain.Page{}, err
		}
		return pageFromRows(l.codec, binding, rows, more)
	}
	page, err := decisions.FilterPage(ctx, l.authorizer, decisions.FilterPageRequest[row]{
		Principal: model.PrincipalFrom(principal), Permission: "view", ResourceType: "document",
		ID: func(r row) string { return r.ID }, Limit: query.Limit, Cursor: query.Cursor, BatchSize: l.batchSize,
		Source: func(ctx context.Context, cursor string, limit int) (decisions.CandidatePage[row], error) {
			position, err := l.codec.decode(cursor, binding)
			if err != nil {
				return decisions.CandidatePage[row]{}, err
			}
			rows, more, err := l.reader.read(ctx, query, position, limit, decisions.ResourceSet{Unrestricted: true})
			if err != nil {
				return decisions.CandidatePage[row]{}, err
			}
			candidates := make([]decisions.Candidate[row], 0, len(rows))
			for _, r := range rows {
				next, err := l.codec.encode(rowPosition(r), binding)
				if err != nil {
					return decisions.CandidatePage[row]{}, err
				}
				candidates = append(candidates, decisions.Candidate[row]{Item: r, NextCursor: next})
			}
			return decisions.CandidatePage[row]{Items: candidates, HasMore: more}, nil
		},
	})
	if err != nil {
		return domain.Page{}, err
	}
	out := domain.Page{Items: make([]domain.Document, 0, len(page.Items)), HasMore: page.HasMore, NextCursor: page.NextCursor, ScanLimitReached: page.ScanLimitReached}
	for _, r := range page.Items {
		out.Items = append(out.Items, r.document())
	}
	return out, nil
}

func checkBypass(ctx context.Context, bypass Bypass, principal sdk.Principal) (bool, error) {
	if bypass == nil {
		return false, nil
	}
	return bypass(ctx, principal)
}

func rowPosition(r row) position { return position{NameKey: r.NameKey, ID: r.ID} }

func pageFromRows(codec *CursorCodec, binding []byte, rows []row, more bool) (domain.Page, error) {
	out := domain.Page{Items: make([]domain.Document, 0, len(rows)), HasMore: more}
	for _, r := range rows {
		out.Items = append(out.Items, r.document())
	}
	if more && len(rows) > 0 {
		cursor, err := codec.encode(rowPosition(rows[len(rows)-1]), binding)
		if err != nil {
			return domain.Page{}, err
		}
		out.NextCursor = cursor
	}
	return out, nil
}
