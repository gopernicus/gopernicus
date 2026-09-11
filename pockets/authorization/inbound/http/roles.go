package authorizationhttp

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
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
type roleCommandRequest struct {
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	Role         string `json:"role"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

// Success responses describe this application. Refusals return a domain error.
type assignResponse struct {
	Outcome mutations.Outcome `json:"outcome"`
}
type unassignResponse struct {
	Outcome              mutations.Outcome `json:"outcome"`
	SameRoleGrantRemains bool              `json:"same_role_grant_remains"`
}

type assignmentResponse struct {
	SubjectType  string `json:"subject_type"`
	SubjectID    string `json:"subject_id"`
	Role         string `json:"role"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

func newAssignmentResponse(a roles.Assignment) assignmentResponse {
	return assignmentResponse{
		SubjectType:  a.SubjectType,
		SubjectID:    a.SubjectID,
		Role:         a.Role,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
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

func newEffectiveGrantResponse(g roles.EffectiveGrant) effectiveGrantResponse {
	return effectiveGrantResponse{
		SubjectType: g.SubjectType,
		SubjectID:   g.SubjectID,
		Role:        g.Role,
		Direct:      g.Direct,
		Global:      g.Global,
	}
}

// pageResponse is the JSON envelope for a paginated list, mirroring list.Page.
type pageResponse[T any] struct {
	Items          []T    `json:"items"`
	NextCursor     string `json:"next_cursor,omitempty"`
	HasMore        bool   `json:"has_more,omitempty"`
	HasPrev        bool   `json:"has_prev,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
	Total          *int64 `json:"total,omitempty"`
}

func newPageResponse[E any, T any](p list.Page[E], mapFn func(E) T) pageResponse[T] {
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
func (h *Adapter) assignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := currentPrincipal(w, r)
	if !ok {
		return
	}
	body, ok := decodeRoleCommand(w, r)
	if !ok {
		return
	}
	cmd := mutations.AssignRoleCommand{
		Subject: authmodel.PrincipalRef{Type: body.SubjectType, ID: body.SubjectID},
		Role:    body.Role, ResourceType: body.ResourceType, ResourceID: body.ResourceID,
	}
	if h.assignmentPolicy != nil {
		if err := h.assignmentPolicy(r.Context(), cmd); err != nil {
			RespondError(w, err)
			return
		}
	}
	result, err := h.mutations.AssignRole(r.Context(), mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: actor.Type, ID: actor.ID}}, cmd)
	if err != nil {
		RespondError(w, err)
		return
	}
	if result == nil {
		web.RespondJSONError(w, web.ErrInternal("role assignment returned no result"))
		return
	}
	web.RespondJSONOK(w, assignResponse{Outcome: result.Outcome})
}

// unassignRole removes a principal's exact role assignment at the given scope.
// Unassigning an absent assignment is a committed not_found no-op on 200, not an
// error.
func (h *Adapter) unassignRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := currentPrincipal(w, r)
	if !ok {
		return
	}
	body, ok := decodeRoleCommand(w, r)
	if !ok {
		return
	}
	cmd := mutations.UnassignRoleCommand{
		Subject: authmodel.PrincipalRef{Type: body.SubjectType, ID: body.SubjectID},
		Role:    body.Role, ResourceType: body.ResourceType, ResourceID: body.ResourceID,
	}
	result, err := h.mutations.UnassignRole(r.Context(), mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: actor.Type, ID: actor.ID}}, cmd)
	if err != nil {
		RespondError(w, err)
		return
	}
	web.RespondJSONOK(w, unassignResponse{
		Outcome:              result.Outcome,
		SameRoleGrantRemains: result.SameRoleGrantRemains,
	})
}

// listBySubject pages a subject's role assignments. A subject's GLOBAL rows
// appear here with empty resource fields.
func (h *Adapter) listBySubject(w http.ResponseWriter, r *http.Request) {
	subjectType, subjectID, ok := requiredPair(w, r, querySubjectType, querySubjectID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, roles.OrderFields, roles.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.roles.ListRoleAssignmentsBySubject(r.Context(), authmodel.PrincipalRef{Type: subjectType, ID: subjectID}, req)
	if err != nil {
		RespondError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newAssignmentResponse))
}

// listByResource pages the RAW direct-scope assignments stored at a resource. It
// never surfaces globally-granted subjects — that is what the effective listing
// is for.
func (h *Adapter) listByResource(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceID, ok := requiredPair(w, r, queryResourceType, queryResourceID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, roles.OrderFields, roles.DefaultOrder)
	if !ok {
		return
	}
	page, err := h.roles.ListRoleAssignmentsByResource(r.Context(), resourceType, resourceID, req)
	if err != nil {
		RespondError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newAssignmentResponse))
}

// listEffectiveByResource pages the EFFECTIVE role grants on a resource — the
// enumeration that agrees with HasRole, which is what an administration UI
// needs.
func (h *Adapter) listEffectiveByResource(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceID, ok := requiredPair(w, r, queryResourceType, queryResourceID)
	if !ok {
		return
	}
	req, ok := h.parseListRequest(w, r, roles.EffectiveOrderFields, roles.DefaultEffectiveOrder)
	if !ok {
		return
	}
	page, err := h.roles.ListEffectiveRoleGrantsByResource(r.Context(), resourceType, resourceID, req)
	if err != nil {
		RespondError(w, err)
		return
	}
	web.RespondJSONOK(w, newPageResponse(page, newEffectiveGrantResponse))
}

// ---------------------------------------------------------------------------
// Transport helpers
// ---------------------------------------------------------------------------

// currentPrincipal reads the principal the host gate's authenticating layer
// stashed with sdk.WithPrincipal. Absence is 401 — never a zero-value
// actor, which the domain would reject later and far less legibly. This is why
// the gate is REQUIRED to include an authenticating layer: the pocket owns no
// credential and adds none.
func currentPrincipal(w http.ResponseWriter, r *http.Request) (sdk.Principal, bool) {
	p, ok := sdk.PrincipalFromContext(r.Context())
	if !ok {
		web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
		return sdk.Principal{}, false
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
// (limit/cursor/offset/count plus the per-listing order) into a list.Request.
//
// A non-empty `q` is rejected BY NAME here rather than forwarded: the role
// listings declare no search fields, so a silent drop would answer an unfiltered
// page to a caller who asked for a filtered one, and forwarding it would surface
// a confusing rejection from the store edge.
func (h *Adapter) parseListRequest(w http.ResponseWriter, r *http.Request, orderFields map[string]list.OrderField, defaultOrder list.Order) (list.Request, bool) {
	q := r.URL.Query()
	if q.Get(list.QueryKeySearch) != "" {
		web.RespondJSONError(w, web.ErrBadRequest("role listings declare no search fields; the q parameter is not supported"))
		return list.Request{}, false
	}
	req, err := list.ParseQuery(q, list.QueryOptions{DefaultStrategy: h.listStrategy})
	if err == nil {
		req.Order, err = list.ParseOrder(orderFields, q.Get(list.QueryKeyOrder), defaultOrder)
	}
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return list.Request{}, false
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
	err := dec.Decode(dst)
	message := "invalid request body"
	if err == nil {
		var trailing json.RawMessage
		err = dec.Decode(&trailing)
		if errors.Is(err, io.EOF) {
			return true
		}
		message = "unexpected trailing data in request body"
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		web.RespondJSONError(w, web.ErrPayloadTooLarge("request body too large"))
	} else {
		web.RespondJSONError(w, web.ErrBadRequest(message))
	}
	return false
}
