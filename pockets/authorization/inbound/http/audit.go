package authorizationhttp

import (
	"context"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The optional exact filter pairs of the audit listing, beside the resource and
// subject keys the role listings share.
const (
	queryActorType = "actor_type"
	queryActorID   = "actor_id"
)

// codeOffsetNotSupported names the refusal of an offset on the cursor-only
// audit listing.
const codeOffsetNotSupported = "offset_not_supported"

// AuditReadEvent describes one completed audit listing read. Filter holds the
// exact pairs the caller supplied; Limit is the resolved page size and Returned
// the number of records written. It carries identifiers only, never records.
type AuditReadEvent struct {
	Principal sdk.Principal
	Filter    audit.Filter
	Limit     int
	Returned  int
}

// AuditReadObserver receives every completed audit listing read. It runs on
// the request path and must not block; the response does not wait on durable
// recording beyond what the observer itself does.
type AuditReadObserver func(context.Context, AuditReadEvent)

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

type auditSourceResponse struct {
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	System    string `json:"system,omitempty"`
	Reason    string `json:"reason,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

type auditChangeResponse struct {
	Action string       `json:"action"`
	Tuple  tuples.Tuple `json:"tuple"`
}

type auditRecordResponse struct {
	ID         string              `json:"id"`
	EventID    string              `json:"event_id"`
	OccurredAt string              `json:"occurred_at"`
	Source     auditSourceResponse `json:"source"`
	Change     auditChangeResponse `json:"change"`
}

func newAuditRecordResponse(r audit.Record) auditRecordResponse {
	return auditRecordResponse{
		ID:         r.ID,
		EventID:    r.EventID,
		OccurredAt: r.OccurredAt.UTC().Format(time.RFC3339Nano),
		Source: auditSourceResponse{
			ActorType: r.Source.ActorType, ActorID: r.Source.ActorID, System: r.Source.System,
			Reason: r.Source.Reason, RequestID: r.Source.RequestID, TraceID: r.Source.TraceID,
		},
		Change: auditChangeResponse{Action: string(r.Change.Action), Tuple: r.Change.Tuple},
	}
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

// listAudit pages the committed change history, newest first. Every filter pair
// is optional, so an unfiltered call lists all history; the AuditGate decides
// who may ask. The listing is cursor only: paging until has_more is false is the
// export. A changed filter needs a fresh cursor.
func (h *Adapter) listAudit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := currentPrincipal(w, r)
	if !ok {
		return
	}
	var filter audit.Filter
	if filter.ResourceType, filter.ResourceID, ok = optionalPair(w, r, queryResourceType, queryResourceID); !ok {
		return
	}
	if filter.SubjectType, filter.SubjectID, ok = optionalPair(w, r, querySubjectType, querySubjectID); !ok {
		return
	}
	if filter.ActorType, filter.ActorID, ok = optionalPair(w, r, queryActorType, queryActorID); !ok {
		return
	}
	if err := filter.Validate(); err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return
	}
	req, ok := parseAuditListRequest(w, r)
	if !ok {
		return
	}
	page, err := h.audit.List(r.Context(), filter, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if h.auditRead != nil {
		h.auditRead(r.Context(), AuditReadEvent{Principal: actor, Filter: filter, Limit: req.Limit, Returned: len(page.Items)})
	}
	web.RespondJSONOK(w, newPageResponse(page, newAuditRecordResponse))
}

// optionalPair reads two query values that are both empty (no filter) or both
// non-empty; one alone is a named 400.
func optionalPair(w http.ResponseWriter, r *http.Request, firstKey, secondKey string) (string, string, bool) {
	q := r.URL.Query()
	first, second := q.Get(firstKey), q.Get(secondKey)
	if (first == "") != (second == "") {
		web.RespondJSONError(w, web.ErrBadRequest(firstKey+" and "+secondKey+" must be supplied together"))
		return "", "", false
	}
	return first, second, true
}

// parseAuditListRequest admits limit, cursor, count and order=occurred_at only.
// The cursor strategy is fixed whatever the role routes' ListStrategy says.
func parseAuditListRequest(w http.ResponseWriter, r *http.Request) (list.Request, bool) {
	q := r.URL.Query()
	if q.Has(list.QueryKeyOffset) {
		web.RespondJSONError(w, web.ErrBadRequest("the audit listing pages by cursor only; the offset parameter is not supported").WithCode(codeOffsetNotSupported))
		return list.Request{}, false
	}
	if q.Get(list.QueryKeySearch) != "" {
		web.RespondJSONError(w, web.ErrBadRequest("the audit listing declares no search fields; the q parameter is not supported"))
		return list.Request{}, false
	}
	req, err := list.ParseQuery(q, list.QueryOptions{DefaultStrategy: list.StrategyCursor})
	if err == nil {
		req.Order, err = list.ParseOrder(audit.OrderFields, q.Get(list.QueryKeyOrder), audit.DefaultOrder)
	}
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return list.Request{}, false
	}
	return req, true
}
