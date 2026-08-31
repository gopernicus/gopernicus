package authorization

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// maxJSONBodyBytes bounds a role-administration request body before decoding, so
// an oversized upload is rejected with 413 rather than buffered whole.
const maxJSONBodyBytes = 1 << 20 // 1 MiB

// The query keys the two scoped listings require. Each listing demands BOTH of
// its values non-empty: an empty pair would enumerate the GLOBAL scope, which
// the bundled surface deliberately does not expose (a host that wants it writes
// its own route over Service.ListRoleAssignmentsByResource).
const (
	querySubjectType  = "subject_type"
	querySubjectID    = "subject_id"
	queryResourceType = "resource_type"
	queryResourceID   = "resource_id"
)

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

// roleCommandRequest is the shared body of both writes. The two domain commands
// are field-identical, so one wire struct describes both honestly.
//
// mutation_id is optional: absent, the server mints an unguessable one and the
// request is distinct from every other; present, it is the client's own
// idempotency key, validated for strength and deduped against the stored
// receipt on retry. expected_revision is the optional compare-and-set anchor.
type roleCommandRequest struct {
	MutationID       string  `json:"mutation_id"`
	SubjectType      string  `json:"subject_type"`
	SubjectID        string  `json:"subject_id"`
	Role             string  `json:"role"`
	ResourceType     string  `json:"resource_type"`
	ResourceID       string  `json:"resource_id"`
	ExpectedRevision *uint64 `json:"expected_revision"`
}

// receiptResponse is the EXPLICIT wire projection of a mutation receipt — never
// a marshal of the domain type. payload_encoding, payload_digest, and
// schema_digest are deliberately off the v1 wire: they are internal integrity
// metadata, additive later if a host needs them.
type receiptResponse struct {
	MutationID string `json:"mutation_id"`
	ScopeKind  string `json:"scope_kind"`
	ScopeType  string `json:"scope_type"`
	ScopeID    string `json:"scope_id"`
	Operation  string `json:"operation"`
	Outcome    string `json:"outcome"`
	Revision   uint64 `json:"revision"`
	Replayed   bool   `json:"replayed"`
	CreatedAt  string `json:"created_at"`
}

func newReceiptResponse(r mutation.Receipt) receiptResponse {
	return receiptResponse{
		MutationID: string(r.MutationID),
		ScopeKind:  string(r.Scope.Kind),
		ScopeType:  r.Scope.Type,
		ScopeID:    r.Scope.ID,
		Operation:  string(r.Operation),
		Outcome:    string(r.Outcome),
		Revision:   uint64(r.Revision),
		Replayed:   r.Replayed,
		CreatedAt:  r.CreatedAt.Format(time.RFC3339),
	}
}

// assignResponse is the assign envelope. Every domain outcome — including
// semantic_conflict, invariant_blocked, and not_found — rides 200 with the
// outcome named in the receipt: a conflict is an OUTCOME, never an error.
type assignResponse struct {
	Receipt receiptResponse `json:"receipt"`
}

// unassignResponse adds the op-specific same_role_grant_remains annotation,
// which appears TOP-LEVEL and only here: it is a statement about this unassign,
// not a receipt field.
type unassignResponse struct {
	Receipt              receiptResponse `json:"receipt"`
	SameRoleGrantRemains bool            `json:"same_role_grant_remains"`
}

type assignmentResponse struct {
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	Role         string `json:"role"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	CreatedAt    string `json:"created_at"`
}

func newAssignmentResponse(a role.Assignment) assignmentResponse {
	return assignmentResponse{
		SubjectType:  a.SubjectType,
		SubjectID:    a.SubjectID,
		Role:         a.Role,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
		CreatedAt:    a.CreatedAt.Format(time.RFC3339),
	}
}

// effectiveGrantResponse carries the grant identity plus its provenance. A
// global grant is never rewritten as a scoped row, so there is no resource pair
// here.
type effectiveGrantResponse struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Role        string `json:"role"`
	Direct      bool   `json:"direct"`
	Global      bool   `json:"global"`
}

func newEffectiveGrantResponse(g role.EffectiveGrant) effectiveGrantResponse {
	return effectiveGrantResponse{
		SubjectType: g.SubjectType,
		SubjectID:   g.SubjectID,
		Role:        g.Role,
		Direct:      g.Direct,
		Global:      g.Global,
	}
}

// pageResponse is the JSON envelope for a paginated list, mirroring crud.Page.
type pageResponse[T any] struct {
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	Total          *int64 `json:"total,omitempty"`
}

func newPageResponse[E any, T any](p crud.Page[E], mapFn func(E) T) pageResponse[T] {
	items := make([]T, 0, len(p.Items))
	for _, e := range p.Items {
		items = append(items, mapFn(e))
	}
	return pageResponse[T]{
		Items:          items,
		NextCursor:     p.NextCursor,
		HasMore:        p.HasMore,
		HasPrev:        p.HasPrev,
		PreviousCursor: p.PreviousCursor,
		Total:          p.Total,
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// assignRole grants a principal a role, globally (both resource fields empty) or
// scoped to a resource (both set). The host gate has already authenticated and
// authorized the request; this handler derives the ACTOR from the principal the
// gate stashed and forwards the command.
func (h *handlers) assignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := currentPrincipal(w, r)
	if !ok {
		return
	}
	body, ok := decodeRoleCommand(w, r)
	if !ok {
		return
	}
	receipt, err := h.svc.AssignRole(r.Context(), AssignRoleRequest{
		ActorType:        actor.Type,
		ActorID:          actor.ID,
		MutationID:       body.MutationID,
		SubjectType:      body.SubjectType,
		SubjectID:        body.SubjectID,
		Role:             body.Role,
		ResourceType:     body.ResourceType,
		ResourceID:       body.ResourceID,
		ExpectedRevision: body.ExpectedRevision,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if receipt == nil {
		web.RespondJSONError(w, web.ErrInternal("role assignment returned no receipt"))
		return
	}
	web.RespondJSONOK(w, assignResponse{Receipt: newReceiptResponse(*receipt)})
}

// unassignRole removes a principal's exact role assignment at the given scope.
// Unassigning an absent assignment is a committed not_found no-op on 200, not an
// error.
func (h *handlers) unassignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := currentPrincipal(w, r)
	if !ok {
		return
	}
	body, ok := decodeRoleCommand(w, r)
	if !ok {
		return
	}
	receipt, sameRoleGrantRemains, err := h.svc.UnassignRole(r.Context(), UnassignRoleRequest{
		ActorType:        actor.Type,
		ActorID:          actor.ID,
		MutationID:       body.MutationID,
		SubjectType:      body.SubjectType,
		SubjectID:        body.SubjectID,
		Role:             body.Role,
		ResourceType:     body.ResourceType,
		ResourceID:       body.ResourceID,
		ExpectedRevision: body.ExpectedRevision,
	})
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	if receipt == nil {
		web.RespondJSONError(w, web.ErrInternal("role unassignment returned no receipt"))
		return
	}
	web.RespondJSONOK(w, unassignResponse{
		Receipt:              newReceiptResponse(*receipt),
		SameRoleGrantRemains: sameRoleGrantRemains,
	})
}

// listBySubject pages a subject's role assignments. A subject's GLOBAL rows
// appear here with empty resource fields.
func (h *handlers) listBySubject(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, ok := requiredPair(w, r, querySubjectType, querySubjectID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, role.OrderFields, role.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListBySubject(r.Context(), subjectType, subjectID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newAssignmentResponse))
}

// listByResource pages the RAW direct-scope assignments stored at a resource. It
// never surfaces globally-granted subjects — that is what the effective listing
// is for.
func (h *handlers) listByResource(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceID, ok := requiredPair(w, r, queryResourceType, queryResourceID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, role.OrderFields, role.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListByResource(r.Context(), resourceType, resourceID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newAssignmentResponse))
}

// listEffectiveByResource pages the EFFECTIVE role grants on a resource — the
// enumeration that agrees with HasRole, which is what an administration UI
// needs.
func (h *handlers) listEffectiveByResource(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceID, ok := requiredPair(w, r, queryResourceType, queryResourceID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, role.EffectiveOrderFields, role.DefaultEffectiveOrder)
	if !ok {
		return
	}
	page, err := h.svc.ListEffectiveByResource(r.Context(), resourceType, resourceID, req)
	if err != nil {
		web.RespondJSONDomainError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newEffectiveGrantResponse))
}

// ---------------------------------------------------------------------------
// Transport helpers
// ---------------------------------------------------------------------------

// currentPrincipal reads the principal the host gate's authenticating layer
// stashed with identity.WithPrincipal. Absence is 401 — never a zero-value
// actor, which the domain would reject later and far less legibly. This is why
// the gate is REQUIRED to include an authenticating layer: the pocket owns no
// credential and adds none.
func currentPrincipal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	p, ok := identity.FromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return identity.Principal{}, false
	}
	return p, true
}

// decodeRoleCommand enforces the JSON content type and decodes the strict,
// bounded body shared by both writes.
func decodeRoleCommand(w http.ResponseWriter, r *http.Request) (roleCommandRequest, bool) {
	if !requireJSON(w, r) {
		return roleCommandRequest{}, false
	}
	var body roleCommandRequest
	if !strictJSONBody(w, r, &body, maxJSONBodyBytes) {
		return roleCommandRequest{}, false
	}
	return body, true
}

// requiredPair reads two query values that must BOTH be non-empty, answering a
// named 400 otherwise. Requiring both is what keeps the global scope
// (`?resource_type=&resource_id=`) off the bundled listings.
func requiredPair(w http.ResponseWriter, r *http.Request, firstKey, secondKey string) (string, string, bool) {
	q := r.URL.Query()
	first, second := q.Get(firstKey), q.Get(secondKey)
	if first == "" || second == "" {
		web.RespondJSONError(w, web.ErrBadRequest(firstKey+" and "+secondKey+" are both required and must be non-empty"))
		return "", "", false
	}
	return first, second, true
}

// parseListRequest parses the strict transport-edge page params
// (limit/cursor/offset/count plus the per-listing order) into a crud.ListRequest.
//
// A non-empty `q` is rejected BY NAME here rather than forwarded: the role
// listings declare no search fields, so a silent drop would answer an unfiltered
// page to a caller who asked for a filtered one, and forwarding it would surface
// a confusing rejection from the store edge.
func (h *handlers) parseListRequest(w http.ResponseWriter, r *http.Request, orderFields map[string]crud.OrderField, defaultOrder crud.Order) (crud.ListRequest, bool) {
	q := r.URL.Query()
	if q.Get(crud.QueryKeySearch) != "" {
		web.RespondJSONError(w, web.ErrBadRequest("role listings declare no search fields; the q parameter is not supported"))
		return crud.ListRequest{}, false
	}
	req, err := crud.ParseListQuery(q, crud.ListQueryOptions{DefaultStrategy: h.listStrategy})
	if err == nil {
		req.Order, err = crud.ParseOrder(orderFields, q.Get(crud.QueryKeyOrder), defaultOrder)
	}
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return crud.ListRequest{}, false
	}
	return req, true
}

// requireJSON rejects a write whose Content-Type is not application/json.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if contentTypeIsJSON(r.Header.Get("Content-Type")) {
		return true
	}
	web.RespondJSONError(w, web.NewError(http.StatusUnsupportedMediaType,
		"content type must be application/json").WithCode("unsupported_media_type"))
	return false
}

// contentTypeIsJSON reports whether a Content-Type header names application/json,
// tolerating parameters (charset) and surrounding space.
func contentTypeIsJSON(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

// strictJSONBody bounds the body with a MaxBytesReader, decodes exactly one JSON
// value into dst rejecting unknown fields, and rejects any trailing data after
// that value. It writes 413 for an oversized body and 400 for malformed,
// unknown-field, or trailing input, returning false on any failure.
func strictJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			web.RespondJSONError(w, web.ErrPayloadTooLarge("request body too large"))
			return false
		}
		web.RespondJSONError(w, web.ErrBadRequest("invalid request body"))
		return false
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		web.RespondJSONError(w, web.ErrBadRequest("unexpected trailing data in request body"))
		return false
	}
	return true
}
