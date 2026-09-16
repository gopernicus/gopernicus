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

// Reader is the principal-free business service consumed by this adapter.
type Reader interface {
	Read(context.Context, domain.Query, domain.Position, int, domain.Restriction) ([]domain.Row, bool, error)
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

func (l *Listing) ListVisible(ctx context.Context, principal sdk.Principal, query Query) (Page, error) {
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
	if unrestricted || l.strategy == CompleteSet {
		filter := decisions.ResourceSet{Unrestricted: unrestricted}
		if !unrestricted {
			filter, err = l.authorizer.LookupAllResourceIDs(ctx, model.PrincipalFrom(principal), "view", "document")
			if err != nil {
				return Page{}, err
			}
		}
		rows, more, err := l.reader.Read(ctx, query.businessQuery(), after, query.Limit, domain.Restriction{Unrestricted: filter.Unrestricted, IDs: filter.IDs})
		if err != nil {
			return Page{}, err
		}
		return pageFromRows(l.codec, binding, rows, more)
	}
	page, err := decisions.FilterPage(ctx, l.authorizer, decisions.FilterPageRequest[domain.Row]{
		Principal: model.PrincipalFrom(principal), Permission: "view", ResourceType: "document",
		ID: func(r domain.Row) string { return r.ID }, Limit: query.Limit, Cursor: query.Cursor, BatchSize: l.batchSize,
		Source: func(ctx context.Context, cursor string, limit int) (decisions.CandidatePage[domain.Row], error) {
			position, err := l.codec.decode(cursor, binding)
			if err != nil {
				return decisions.CandidatePage[domain.Row]{}, err
			}
			rows, more, err := l.reader.Read(ctx, query.businessQuery(), position, limit, domain.Restriction{Unrestricted: true})
			if err != nil {
				return decisions.CandidatePage[domain.Row]{}, err
			}
			candidates := make([]decisions.Candidate[domain.Row], 0, len(rows))
			for _, r := range rows {
				next, err := l.codec.encode(r.Position(), binding)
				if err != nil {
					return decisions.CandidatePage[domain.Row]{}, err
				}
				candidates = append(candidates, decisions.Candidate[domain.Row]{Item: r, NextCursor: next})
			}
			return decisions.CandidatePage[domain.Row]{Items: candidates, HasMore: more}, nil
		},
	})
	if err != nil {
		return Page{}, err
	}
	out := Page{Items: make([]domain.Document, 0, len(page.Items)), HasMore: page.HasMore, NextCursor: page.NextCursor, ScanLimitReached: page.ScanLimitReached}
	for _, r := range page.Items {
		out.Items = append(out.Items, r.Document())
	}
	return out, nil
}

func checkBypass(ctx context.Context, bypass Bypass, principal sdk.Principal) (bool, error) {
	if bypass == nil {
		return false, nil
	}
	return bypass(ctx, principal)
}

func pageFromRows(codec *CursorCodec, binding []byte, rows []domain.Row, more bool) (Page, error) {
	out := Page{Items: make([]domain.Document, 0, len(rows)), HasMore: more}
	for _, r := range rows {
		out.Items = append(out.Items, r.Document())
	}
	if more && len(rows) > 0 {
		cursor, err := codec.encode(rows[len(rows)-1].Position(), binding)
		if err != nil {
			return Page{}, err
		}
		out.NextCursor = cursor
	}
	return out, nil
}
