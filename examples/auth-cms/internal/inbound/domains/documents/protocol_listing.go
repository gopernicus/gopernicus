package documents

import (
	"context"
	"fmt"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// protocolMaxScan bounds the candidates one page may scan; it matches the
// pocket's FilterPage default so both listings stop at the same row.
const protocolMaxScan = model.DefaultMaxFilterScan

// ProtocolListing is the Candidates strategy over the authorization protocol:
// it postfilters host-ordered candidate rows through
// sdkauthorization.FilterAuthorized, so any sdkauthorization.Checker (the
// pocket's Components.Authorizer, or a vendor integration) decides document
// view. Its pages match Listing's Candidates strategy row for row.
type ProtocolListing struct {
	reader    Reader
	checker   sdkauthorization.Checker
	codec     *CursorCodec
	batchSize int
	bypass    Bypass
}

func NewProtocolListing(reader Reader, checker sdkauthorization.Checker, codec *CursorCodec, batchSize int, bypass Bypass) (*ProtocolListing, error) {
	if reader == nil || checker == nil || codec == nil || batchSize < 0 {
		return nil, fmt.Errorf("documents: invalid protocol listing configuration: %w", sdk.ErrInvalidInput)
	}
	return &ProtocolListing{reader: reader, checker: checker, codec: codec, batchSize: batchSize, bypass: bypass}, nil
}

func (l *ProtocolListing) ListVisible(ctx context.Context, principal sdk.Principal, query Query) (Page, error) {
	if err := query.normalize(); err != nil {
		return Page{}, err
	}
	if err := model.ValidatePrincipal(principal); err != nil {
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
	if unrestricted {
		rows, more, err := l.reader.Read(ctx, query.businessQuery(), after, query.Limit, domain.Restriction{Unrestricted: true})
		if err != nil {
			return Page{}, err
		}
		return pageFromRows(l.codec, binding, rows, more)
	}
	pull := l.batchSize
	if pull == 0 {
		pull = query.Limit
	}
	if limiter, ok := l.checker.(sdkauthorization.Limiter); ok {
		pull = min(pull, limiter.Limits().MaxCheckBatch)
	}
	out := Page{Items: []domain.Document{}}
	position, scanned := after, 0
	for len(out.Items) < query.Limit && scanned < protocolMaxScan {
		rows, more, err := l.reader.Read(ctx, query.businessQuery(), position, min(pull, protocolMaxScan-scanned), domain.Restriction{Unrestricted: true})
		if err != nil {
			return Page{}, err
		}
		if len(rows) == 0 {
			out.HasMore = false
			return out, nil
		}
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		allowedIDs, err := sdkauthorization.FilterAuthorized(ctx, l.checker, principal, "view", "document", ids)
		if err != nil {
			return Page{}, err
		}
		allowed := make(map[string]bool, len(allowedIDs))
		for _, id := range allowedIDs {
			allowed[id] = true
		}
		for i, r := range rows {
			scanned++
			position = r.Position()
			if out.NextCursor, err = l.codec.encode(position, binding); err != nil {
				return Page{}, err
			}
			if !allowed[r.ID] {
				continue
			}
			out.Items = append(out.Items, r.Document())
			if len(out.Items) == query.Limit {
				out.HasMore = i < len(rows)-1 || more
				return out, nil
			}
		}
		if !more {
			out.HasMore = false
			return out, nil
		}
	}
	out.HasMore, out.ScanLimitReached = true, true
	return out, nil
}
