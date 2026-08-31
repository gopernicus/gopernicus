package authorization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// roleRoutes is the full bundled surface. Every posture case below asserts the
// SAME answer on all five: a posture protecting four of them would be no
// posture at all.
var roleRoutes = []struct{ method, path string }{
	{"POST", "/authorization/roles"},
	{"POST", "/authorization/roles/unassign"},
	{"GET", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1"},
	{"GET", "/authorization/roles/by-resource?resource_type=organization&resource_id=o-1"},
	{"GET", "/authorization/roles/effective?resource_type=organization&resource_id=o-1"},
}

// stubRoleAdmin records what the handlers forwarded and returns canned answers.
// The real conversion to the root's command vocabulary is the adapter's job and
// is proven at the pocket root; here the port is a stub so the TRANSPORT is what
// is under test.
type stubRoleAdmin struct {
	assignReq   AssignRoleRequest
	unassignReq UnassignRoleRequest
	listReq     crud.ListRequest
	listSubject [2]string
	listScope   [2]string

	receipt   *mutation.Receipt
	remains   bool
	err       error
	page      crud.Page[role.Assignment]
	effective crud.Page[role.EffectiveGrant]
}

func (s *stubRoleAdmin) AssignRole(_ context.Context, req AssignRoleRequest) (*mutation.Receipt, error) {
	s.assignReq = req
	return s.receipt, s.err
}

func (s *stubRoleAdmin) UnassignRole(_ context.Context, req UnassignRoleRequest) (*mutation.Receipt, bool, error) {
	s.unassignReq = req
	return s.receipt, s.remains, s.err
}

func (s *stubRoleAdmin) ListBySubject(_ context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	s.listSubject = [2]string{subjectType, subjectID}
	s.listReq = req
	return s.page, s.err
}

func (s *stubRoleAdmin) ListByResource(_ context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	s.listScope = [2]string{resourceType, resourceID}
	s.listReq = req
	return s.page, s.err
}

func (s *stubRoleAdmin) ListEffectiveByResource(_ context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	s.listScope = [2]string{resourceType, resourceID}
	s.listReq = req
	return s.effective, s.err
}

// testReceipt is a fully-populated receipt so the wire projection can be
// asserted field for field.
func testReceipt() *mutation.Receipt {
	return &mutation.Receipt{
		MutationID:      "mid-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Scope:           mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "organization", ID: "o-1"},
		Operation:       mutation.OpRoleAssign,
		PayloadEncoding: mutation.MutationEncodingVersion,
		PayloadDigest:   "digest-must-not-ship",
		SchemaDigest:    "schema-must-not-ship",
		Outcome:         mutation.OutcomeApplied,
		Revision:        7,
		Replayed:        true,
		CreatedAt:       time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
}

// injectPrincipal stands in for the authenticating layer the host gate is
// REQUIRED to compose: it stashes a principal exactly as
// identity.WithPrincipal does.
func injectPrincipal(p identity.Principal) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
		})
	}
}

// passGate is a gate that authenticates nobody — it proves the handlers answer
// 401 rather than fabricating a zero actor.
func passGate(next http.Handler) http.Handler { return next }

// newFixture mounts the bundled surface behind gate.
func newFixture(svc RoleAdminService, gate web.Middleware, strategy crud.Strategy) http.Handler {
	h := web.NewWebHandler()
	Mount(h, Deps{Service: svc, Gate: gate, ListStrategy: strategy})
	return h
}

// adminGate is the ordinary test posture: an authenticated platform admin.
func adminGate() web.Middleware {
	return injectPrincipal(identity.Principal{Type: "user", ID: "admin-1"})
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestGateWrapsEveryRoute proves the host gate runs on all five routes — a
// counting gate that never forwards would leave the surface unguarded.
func TestGateWrapsEveryRoute(t *testing.T) {
	var seen int
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen++
			adminGate()(next).ServeHTTP(w, r)
		})
	}
	stub := &stubRoleAdmin{receipt: testReceipt()}
	h := newFixture(stub, counting, "")
	for _, rt := range roleRoutes {
		doJSON(t, h, rt.method, rt.path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
	}
	if seen != len(roleRoutes) {
		t.Fatalf("gate ran on %d routes, want %d", seen, len(roleRoutes))
	}
}

// TestGateDenialIsTheOnlyResponse proves a refusing gate answers for every
// route and the handlers never run: the pocket writes no 403 of its own.
func TestGateDenialIsTheOnlyResponse(t *testing.T) {
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			web.RespondJSONError(w, web.ErrForbidden("permission denied"))
		})
	}
	stub := &stubRoleAdmin{receipt: testReceipt()}
	h := newFixture(stub, deny, "")
	for _, rt := range roleRoutes {
		rec := doJSON(t, h, rt.method, rt.path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", rt.method, rt.path, rec.Code)
		}
	}
	if stub.assignReq.ActorID != "" || stub.unassignReq.ActorID != "" {
		t.Error("a denied request reached the service")
	}
}

// TestMissingPrincipalIs401 proves the writes refuse rather than fabricate a
// zero actor when the gate carried no authenticating layer.
func TestMissingPrincipalIs401(t *testing.T) {
	stub := &stubRoleAdmin{receipt: testReceipt()}
	h := newFixture(stub, passGate, "")
	for _, path := range []string{"/authorization/roles", "/authorization/roles/unassign"} {
		rec := doJSON(t, h, "POST", path, `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s = %d, want 401", path, rec.Code)
		}
	}
	if stub.assignReq.ActorID != "" || stub.unassignReq.ActorID != "" {
		t.Error("an unauthenticated request reached the service")
	}
}

// TestAssignForwardsActorAndCommand pins what the handler hands the port.
func TestAssignForwardsActorAndCommand(t *testing.T) {
	stub := &stubRoleAdmin{receipt: testReceipt()}
	h := newFixture(stub, adminGate(), "")
	rev := uint64(3)
	body := `{"mutation_id":"client-supplied-id","subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1","expected_revision":3}`
	rec := doJSON(t, h, "POST", "/authorization/roles", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := AssignRoleRequest{
		ActorType: "user", ActorID: "admin-1",
		MutationID:  "client-supplied-id",
		SubjectType: "user", SubjectID: "u-1",
		Role:         "viewer",
		ResourceType: "organization", ResourceID: "o-1",
		ExpectedRevision: &rev,
	}
	got := stub.assignReq
	if got.ActorType != want.ActorType || got.ActorID != want.ActorID ||
		got.MutationID != want.MutationID || got.SubjectType != want.SubjectType ||
		got.SubjectID != want.SubjectID || got.Role != want.Role ||
		got.ResourceType != want.ResourceType || got.ResourceID != want.ResourceID {
		t.Errorf("forwarded %+v, want %+v", got, want)
	}
	if got.ExpectedRevision == nil || *got.ExpectedRevision != rev {
		t.Errorf("expected_revision = %v, want %d", got.ExpectedRevision, rev)
	}
}

// TestAssignReceiptWireShape pins the receipt envelope exactly: the nine
// documented fields, and none of the integrity digests.
func TestAssignReceiptWireShape(t *testing.T) {
	stub := &stubRoleAdmin{receipt: testReceipt()}
	h := newFixture(stub, adminGate(), "")
	rec := doJSON(t, h, "POST", "/authorization/roles", `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if _, ok := envelope["same_role_grant_remains"]; ok {
		t.Error("same_role_grant_remains appears on the ASSIGN envelope; it is unassign-only")
	}
	raw, ok := envelope["receipt"]
	if !ok {
		t.Fatal("no receipt in the assign envelope")
	}
	var receipt map[string]any
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	want := map[string]any{
		"mutation_id": "mid-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"scope_kind":  "resource",
		"scope_type":  "organization",
		"scope_id":    "o-1",
		"operation":   "role_assign",
		"outcome":     "applied",
		"revision":    float64(7),
		"replayed":    true,
		"created_at":  "2026-08-31T12:00:00Z",
	}
	if len(receipt) != len(want) {
		t.Errorf("receipt has %d fields %v, want exactly %d", len(receipt), receipt, len(want))
	}
	for k, v := range want {
		if receipt[k] != v {
			t.Errorf("receipt[%q] = %v, want %v", k, receipt[k], v)
		}
	}
	for _, off := range []string{"payload_encoding", "payload_digest", "schema_digest"} {
		if _, leaked := receipt[off]; leaked {
			t.Errorf("%s is on the v1 wire; it is deliberately off", off)
		}
	}
}

// TestUnassignCarriesSameRoleGrantRemains proves the annotation is top-level on
// the unassign envelope and honest about its value.
func TestUnassignCarriesSameRoleGrantRemains(t *testing.T) {
	for _, remains := range []bool{true, false} {
		stub := &stubRoleAdmin{receipt: testReceipt(), remains: remains}
		h := newFixture(stub, adminGate(), "")
		rec := doJSON(t, h, "POST", "/authorization/roles/unassign", `{"subject_type":"user","subject_id":"u-1","role":"viewer","resource_type":"organization","resource_id":"o-1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var got struct {
			Receipt              map[string]any `json:"receipt"`
			SameRoleGrantRemains bool           `json:"same_role_grant_remains"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.SameRoleGrantRemains != remains {
			t.Errorf("same_role_grant_remains = %v, want %v", got.SameRoleGrantRemains, remains)
		}
		if _, inReceipt := got.Receipt["same_role_grant_remains"]; inReceipt {
			t.Error("same_role_grant_remains is inside the receipt; it belongs top-level only")
		}
	}
}

// TestConflictOutcomesRideTwoHundred pins mutation default #8 at the transport:
// every domain outcome is an OUTCOME on 200, never an error status.
func TestConflictOutcomesRideTwoHundred(t *testing.T) {
	outcomes := []mutation.Outcome{
		mutation.OutcomeApplied,
		mutation.OutcomeNoChange,
		mutation.OutcomeSemanticConflict,
		mutation.OutcomeInvariantBlocked,
		mutation.OutcomeNotFound,
	}
	for _, outcome := range outcomes {
		receipt := testReceipt()
		receipt.Outcome = outcome
		stub := &stubRoleAdmin{receipt: receipt}
		h := newFixture(stub, adminGate(), "")
		rec := doJSON(t, h, "POST", "/authorization/roles", `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("outcome %q → %d, want 200", outcome, rec.Code)
		}
		var got assignResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Receipt.Outcome != string(outcome) {
			t.Errorf("outcome on the wire = %q, want %q", got.Receipt.Outcome, outcome)
		}
	}
}

// TestServiceErrorsMapThroughDomainError proves the FS9 mapping: a service
// refusal wrapping an sdk kind lands on that kind's status, and an unwrapped
// one lands 500 by design.
func TestServiceErrorsMapThroughDomainError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"malformed mutation id", fmt.Errorf("mutation id too weak: %w", sdk.ErrInvalidInput), http.StatusBadRequest},
		{"guard denial", fmt.Errorf("denied: %w", sdk.ErrForbidden), http.StatusForbidden},
		{"unwrapped policy error", fmt.Errorf("host policy blew up"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubRoleAdmin{err: tc.err}
			h := newFixture(stub, adminGate(), "")
			rec := doJSON(t, h, "POST", "/authorization/roles", `{"subject_type":"user","subject_id":"u-1","role":"viewer"}`)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestWriteBodyHardening pins the strict-body contract on both writes.
func TestWriteBodyHardening(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        int
	}{
		{"unknown field", "application/json", `{"subject_type":"user","surprise":1}`, http.StatusBadRequest},
		{"malformed json", "application/json", `{"subject_type":`, http.StatusBadRequest},
		{"trailing data", "application/json", `{"subject_type":"user"}{"more":1}`, http.StatusBadRequest},
		{"wrong content type", "text/plain", `{"subject_type":"user"}`, http.StatusUnsupportedMediaType},
		{"oversized body", "application/json", `{"role":"` + strings.Repeat("x", maxJSONBodyBytes+1) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		for _, path := range []string{"/authorization/roles", "/authorization/roles/unassign"} {
			t.Run(tc.name+" "+path, func(t *testing.T) {
				stub := &stubRoleAdmin{receipt: testReceipt()}
				h := newFixture(stub, adminGate(), "")
				req := httptest.NewRequest("POST", path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contentType)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
				}
				if stub.assignReq.ActorID != "" || stub.unassignReq.ActorID != "" {
					t.Error("a malformed request reached the service")
				}
			})
		}
	}
}

// TestListingsRequireBothQueryValues proves the global scope is unreachable
// through the bundled listings: a half pair — or an explicitly empty pair — is a
// named 400, never an enumeration of every global assignment.
func TestListingsRequireBothQueryValues(t *testing.T) {
	cases := []string{
		"/authorization/roles/by-subject",
		"/authorization/roles/by-subject?subject_type=user",
		"/authorization/roles/by-subject?subject_id=u-1",
		"/authorization/roles/by-subject?subject_type=&subject_id=",
		"/authorization/roles/by-resource",
		"/authorization/roles/by-resource?resource_type=organization",
		"/authorization/roles/by-resource?resource_id=o-1",
		"/authorization/roles/by-resource?resource_type=&resource_id=",
		"/authorization/roles/effective",
		"/authorization/roles/effective?resource_type=organization",
		"/authorization/roles/effective?resource_id=o-1",
		"/authorization/roles/effective?resource_type=&resource_id=",
	}
	for _, path := range cases {
		stub := &stubRoleAdmin{}
		h := newFixture(stub, adminGate(), "")
		rec := doJSON(t, h, "GET", path, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
		if stub.listSubject != [2]string{} || stub.listScope != [2]string{} {
			t.Errorf("GET %s reached the service", path)
		}
	}
}

// TestListingsRejectSearch proves `q` is refused by name rather than silently
// dropped: the role listings declare no search fields.
func TestListingsRejectSearch(t *testing.T) {
	paths := []string{
		"/authorization/roles/by-subject?subject_type=user&subject_id=u-1&q=admin",
		"/authorization/roles/by-resource?resource_type=organization&resource_id=o-1&q=admin",
		"/authorization/roles/effective?resource_type=organization&resource_id=o-1&q=admin",
	}
	for _, path := range paths {
		stub := &stubRoleAdmin{}
		h := newFixture(stub, adminGate(), "")
		rec := doJSON(t, h, "GET", path, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "no search fields") {
			t.Errorf("GET %s body = %s, want the named search refusal", path, rec.Body.String())
		}
	}
}

// TestListingsParseOrderAgainstTheirAllowList proves each listing validates the
// order parameter against ITS OWN allow-list — created_at for the raw listings,
// grant_key for the effective one — and that the defaults are applied.
func TestListingsParseOrderAgainstTheirAllowList(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantOrder crud.Order
		wantCode  int
	}{
		{"by-subject default", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1", role.DefaultOrder, http.StatusOK},
		{"by-subject explicit", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1&order=created_at:asc", crud.NewOrder("created_at", crud.ASC), http.StatusOK},
		{"by-subject unknown field", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1&order=grant_key", crud.Order{}, http.StatusBadRequest},
		{"by-resource default", "/authorization/roles/by-resource?resource_type=organization&resource_id=o-1", role.DefaultOrder, http.StatusOK},
		{"effective default", "/authorization/roles/effective?resource_type=organization&resource_id=o-1", role.DefaultEffectiveOrder, http.StatusOK},
		{"effective explicit", "/authorization/roles/effective?resource_type=organization&resource_id=o-1&order=grant_key:desc", crud.NewOrder("grant_key", crud.DESC), http.StatusOK},
		{"effective rejects the raw field", "/authorization/roles/effective?resource_type=organization&resource_id=o-1&order=created_at", crud.Order{}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubRoleAdmin{}
			h := newFixture(stub, adminGate(), "")
			rec := doJSON(t, h, "GET", tc.path, "")
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			if stub.listReq.Order != tc.wantOrder {
				t.Errorf("order = %+v, want %+v", stub.listReq.Order, tc.wantOrder)
			}
		})
	}
}

// TestListingsParseLimitAndStrategy proves the page params reach the port and
// that the host-configured DefaultStrategy applies when a request names neither
// a cursor nor an offset.
func TestListingsParseLimitAndStrategy(t *testing.T) {
	stub := &stubRoleAdmin{}
	h := newFixture(stub, adminGate(), crud.StrategyOffset)
	rec := doJSON(t, h, "GET", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1&limit=7", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.listReq.Limit != 7 {
		t.Errorf("limit = %d, want 7", stub.listReq.Limit)
	}
	if stub.listReq.ResolvedStrategy() != crud.StrategyOffset {
		t.Errorf("strategy = %q, want offset", stub.listReq.ResolvedStrategy())
	}

	cursorHost := newFixture(&stubRoleAdmin{}, adminGate(), "")
	if rec := doJSON(t, cursorHost, "GET", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1&limit=0", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0 → %d, want 400", rec.Code)
	}
}

// TestListingWireShapes pins the assignment and effective-grant DTOs and the
// page envelope.
func TestListingWireShapes(t *testing.T) {
	total := int64(2)
	stub := &stubRoleAdmin{
		page: crud.Page[role.Assignment]{
			Items: []role.Assignment{{
				SubjectType: "user", SubjectID: "u-1", Role: "viewer",
				ResourceType: "organization", ResourceID: "o-1",
				CreatedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
			}},
			NextCursor: "cur", HasMore: true, Total: &total,
		},
		effective: crud.Page[role.EffectiveGrant]{
			Items: []role.EffectiveGrant{{SubjectType: "user", SubjectID: "u-2", Role: "steward", Direct: false, Global: true}},
		},
	}
	h := newFixture(stub, adminGate(), "")

	rec := doJSON(t, h, "GET", "/authorization/roles/by-subject?subject_type=user&subject_id=u-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
		HasMore    bool             `json:"has_more"`
		Total      *int64           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.NextCursor != "cur" || !raw.HasMore || raw.Total == nil || *raw.Total != total {
		t.Errorf("page envelope = %+v, want the crud.Page projection", raw)
	}
	wantItem := map[string]any{
		"subject_type": "user", "subject_id": "u-1", "role": "viewer",
		"resource_type": "organization", "resource_id": "o-1",
		"created_at": "2026-08-31T12:00:00Z",
	}
	if len(raw.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(raw.Items))
	}
	if len(raw.Items[0]) != len(wantItem) {
		t.Errorf("assignment has %d fields %v, want exactly %d", len(raw.Items[0]), raw.Items[0], len(wantItem))
	}
	for k, v := range wantItem {
		if raw.Items[0][k] != v {
			t.Errorf("assignment[%q] = %v, want %v", k, raw.Items[0][k], v)
		}
	}

	rec = doJSON(t, h, "GET", "/authorization/roles/effective?resource_type=organization&resource_id=o-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var grants struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &grants); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantGrant := map[string]any{"subject_type": "user", "subject_id": "u-2", "role": "steward", "direct": false, "global": true}
	if len(grants.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(grants.Items))
	}
	if len(grants.Items[0]) != len(wantGrant) {
		t.Errorf("effective grant has %d fields %v, want exactly %d", len(grants.Items[0]), grants.Items[0], len(wantGrant))
	}
	for k, v := range wantGrant {
		if grants.Items[0][k] != v {
			t.Errorf("grant[%q] = %v, want %v", k, grants.Items[0][k], v)
		}
	}
}

// TestListingsForwardTheirScope proves each path forwards the query pair it
// requires to the port unchanged.
func TestListingsForwardTheirScope(t *testing.T) {
	stub := &stubRoleAdmin{}
	h := newFixture(stub, adminGate(), "")

	doJSON(t, h, "GET", "/authorization/roles/by-subject?subject_type=service_account&subject_id=sa-9", "")
	if stub.listSubject != [2]string{"service_account", "sa-9"} {
		t.Errorf("by-subject forwarded %v", stub.listSubject)
	}
	doJSON(t, h, "GET", "/authorization/roles/by-resource?resource_type=section&resource_id=s-4", "")
	if stub.listScope != [2]string{"section", "s-4"} {
		t.Errorf("by-resource forwarded %v", stub.listScope)
	}
	doJSON(t, h, "GET", "/authorization/roles/effective?resource_type=page&resource_id=p-2", "")
	if stub.listScope != [2]string{"page", "p-2"} {
		t.Errorf("effective forwarded %v", stub.listScope)
	}
}
