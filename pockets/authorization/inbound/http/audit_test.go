package authorizationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const auditWarnText = "audit reads are not recorded; wire WithAuditReadObserver"

type stubAuditReader struct {
	filter audit.Filter
	req    list.Request
	calls  int
	page   list.Page[audit.Record]
	err    error
}

func (s *stubAuditReader) List(_ context.Context, filter audit.Filter, req list.Request) (list.Page[audit.Record], error) {
	s.calls++
	s.filter, s.req = filter, req
	return s.page, s.err
}

type auditEvents struct{ got []AuditReadEvent }

func (e *auditEvents) observe(_ context.Context, ev AuditReadEvent) { e.got = append(e.got, ev) }

func auditLogger() (*slog.Logger, *bytes.Buffer) {
	var logs bytes.Buffer
	return slog.New(slog.NewTextHandler(&logs, nil)), &logs
}

func auditRouter(t *testing.T, reader audit.Reader, gate web.Middleware, events *auditEvents) http.Handler {
	t.Helper()
	opts := []Option{WithRoleRoutes(RoleRoutes{AuditGate: gate})}
	if events != nil {
		opts = append(opts, WithAuditReadObserver(events.observe))
	}
	a, err := New(Services{Audit: reader}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	if err := a.Register(h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAuditGateConstruction(t *testing.T) {
	if _, err := New(Services{}, WithRoleRoutes(RoleRoutes{AuditGate: adminGate()})); !errors.Is(err, ErrAuditGateWithoutReader) {
		t.Fatalf("gate without reader: %v", err)
	}
	var typed *memory.Audit
	if _, err := New(Services{Audit: typed}, WithRoleRoutes(RoleRoutes{AuditGate: adminGate()})); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil reader: %v", err)
	}
	for _, tc := range []struct {
		name     string
		gate     web.Middleware
		observer AuditReadObserver
		warns    int
	}{
		{"gate without observer", adminGate(), nil, 1},
		{"gate with observer", adminGate(), func(context.Context, AuditReadEvent) {}, 0},
		{"no gate", nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, logs := auditLogger()
			a, err := New(Services{Audit: &stubAuditReader{}}, WithRoleRoutes(RoleRoutes{AuditGate: tc.gate}), WithAuditReadObserver(tc.observer), WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(logs.String(), auditWarnText); n != tc.warns || strings.Count(logs.String(), "level=WARN") != tc.warns {
				t.Fatalf("warnings=%d want %d: %s", n, tc.warns, logs.String())
			}
			if a.AuditRouteEnabled() != (tc.gate != nil) || a.RoutesEnabled() {
				t.Fatalf("enabled audit=%v roles=%v", a.AuditRouteEnabled(), a.RoutesEnabled())
			}
		})
	}
}

func TestAuditRouteAbsentWithoutGate(t *testing.T) {
	reader := &stubAuditReader{}
	stub := &stubRoleAdmin{receipt: testReceipt()}
	a, err := New(Services{Roles: stub, Mutations: stub, Audit: reader}, WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: allowWrite}))
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	if err := a.Register(h); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, h, "GET", pathAudit, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("ungated audit route status=%d", rec.Code)
	}
	if rec := doJSON(t, a.Audit(), "GET", pathAudit, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("ungated Audit() status=%d", rec.Code)
	}
	if reader.calls != 0 {
		t.Fatal("ungated route read history")
	}
}

func TestAuditGateAloneMountsOnlyTheAuditRoute(t *testing.T) {
	h := auditRouter(t, &stubAuditReader{}, adminGate(), &auditEvents{})
	for _, route := range roleRoutes {
		if rec := doJSON(t, h, route.method, route.path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d", route.method, route.path, rec.Code)
		}
	}
	if rec := doJSON(t, h, "GET", pathAudit, ""); rec.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestAuditRouteRequiresPrincipal(t *testing.T) {
	reader, events := &stubAuditReader{}, &auditEvents{}
	rec := doJSON(t, auditRouter(t, reader, passGate, events), "GET", pathAudit, "")
	if rec.Code != http.StatusUnauthorized || reader.calls != 0 || len(events.got) != 0 {
		t.Fatalf("status=%d calls=%d events=%d", rec.Code, reader.calls, len(events.got))
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
}

func TestAuditRouteProjectsRecordsAndObservesTheRead(t *testing.T) {
	occurred := time.Date(2026, 10, 25, 12, 0, 0, 123456000, time.FixedZone("x", 3600))
	reader := &stubAuditReader{page: list.Page[audit.Record]{
		Items: []audit.Record{{
			Encoding: audit.EncodingTuple, ID: "e1:00000000000000000001", EventID: "e1", OccurredAt: occurred,
			Source: audit.Source{ActorType: "user", ActorID: "admin-1", RequestID: "req-1"},
			Change: audit.Change{Action: audit.ActionAdded, Tuple: tuples.Tuple{Scope: tuples.On("project", "p1"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "u1"}}},
		}},
		NextCursor: "next", HasMore: true,
	}}
	events := &auditEvents{}
	h := auditRouter(t, reader, adminGate(), events)
	rec := doJSON(t, h, "GET", pathAudit+"?resource_type=project&resource_id=p1&actor_type=user&actor_id=admin-1&limit=10&count=true&order=occurred_at:asc&cursor=c1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	wantFilter := audit.Filter{ResourceType: "project", ResourceID: "p1", ActorType: "user", ActorID: "admin-1"}
	if reader.filter != wantFilter {
		t.Fatalf("filter=%+v", reader.filter)
	}
	if reader.req.Limit != 10 || !reader.req.WithCount || reader.req.Cursor != "c1" || reader.req.ResolvedStrategy() != list.StrategyCursor || reader.req.Order != list.NewOrder("occurred_at", list.ASC) {
		t.Fatalf("request=%+v", reader.req)
	}
	want := `{"items":[{"id":"e1:00000000000000000001","event_id":"e1","occurred_at":"2026-10-25T11:00:00.123456Z","source":{"actor_type":"user","actor_id":"admin-1","request_id":"req-1"},"change":{"action":"added","tuple":{"scope":{"kind":"resource","resource_type":"project","resource_id":"p1"},"relation":"owner","subject":{"type":"user","id":"u1"}}}}],"next_cursor":"next","has_more":true}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
	wantEvent := AuditReadEvent{Principal: sdk.Principal{Type: "user", ID: "admin-1"}, Filter: wantFilter, Limit: 10, Returned: 1}
	if len(events.got) != 1 || events.got[0] != wantEvent {
		t.Fatalf("events=%+v", events.got)
	}
	// Reads are never deduplicated.
	doJSON(t, h, "GET", pathAudit+"?resource_type=project&resource_id=p1&actor_type=user&actor_id=admin-1&limit=10&count=true&order=occurred_at:asc&cursor=c1", "")
	if len(events.got) != 2 {
		t.Fatalf("second identical read: %d events", len(events.got))
	}
}

func TestAuditRouteRejectsUnsupportedQueries(t *testing.T) {
	for _, tc := range []struct{ query, code string }{
		{"?resource_type=project", "bad_request"},
		{"?subject_id=u1", "bad_request"},
		{"?actor_type=user", "bad_request"},
		{"?resource_type=project&resource_id=p%091", "bad_request"},
		{"?offset=5", codeOffsetNotSupported},
		{"?offset=", codeOffsetNotSupported},
		{"?q=x", "bad_request"},
		{"?limit=101", "bad_request"},
		{"?limit=0", "bad_request"},
		{"?order=id", "bad_request"},
		{"?count=maybe", "bad_request"},
		{"?resource_type=project&resource_id=p1&resource_id=p2", "bad_request"},
		{"?actor_type=user&actor_type=user&actor_id=a1", "bad_request"},
		{"?limit=2&limit=3", "bad_request"},
		{"?cursor=a&cursor=b", "bad_request"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			reader, events := &stubAuditReader{}, &auditEvents{}
			rec := doJSON(t, auditRouter(t, reader, adminGate(), events), "GET", pathAudit+tc.query, "")
			var body web.Error
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != http.StatusBadRequest || body.Code != tc.code {
				t.Fatalf("status=%d code=%q body=%s", rec.Code, body.Code, rec.Body)
			}
			if reader.calls != 0 || len(events.got) != 0 {
				t.Fatal("refused query read history or emitted an event")
			}
		})
	}
}

func TestAuditRouteReaderErrorsUseDomainMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("store down: %w", sdk.ErrUnavailable), http.StatusServiceUnavailable, "unavailable"},
		{fmt.Errorf("bad cursor: %w", sdk.ErrInvalidInput), http.StatusBadRequest, "bad_request"},
	} {
		reader, events := &stubAuditReader{err: tc.err}, &auditEvents{}
		rec := doJSON(t, auditRouter(t, reader, adminGate(), events), "GET", pathAudit, "")
		var body web.Error
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Code != tc.code || len(events.got) != 0 {
			t.Fatalf("%v: status=%d code=%q events=%d", tc.err, rec.Code, body.Code, len(events.got))
		}
	}
}

// TestAuditRoutePagesRealHistory drives role writes and the listing over one
// memory store: the self-assignment RefuseSelfTarget refuses never reaches the
// history, and cursor paging returns every record exactly once.
func TestAuditRoutePagesRealHistory(t *testing.T) {
	store := memory.New(memory.WithAudit())
	reader, err := roles.NewService(store.Tuples())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := mutations.NewService(store.Mutations())
	if err != nil {
		t.Fatal(err)
	}
	events := &auditEvents{}
	a, err := New(Services{Roles: reader, Mutations: writer, Audit: store.Audit()},
		WithRoleRoutes(RoleRoutes{Gate: adminGate(), WritePolicy: AllOf(RefuseSelfTarget, allowWrite), AuditGate: adminGate()}),
		WithAuditReadObserver(events.observe))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(func() http.Handler {
		h := web.NewWebHandler()
		if err := a.Register(h); err != nil {
			t.Fatal(err)
		}
		return h
	}())
	defer server.Close()
	post := func(subject string) int {
		body := fmt.Sprintf(`{"subject_type":"user","subject_id":%q,"role":"viewer","scope":{"kind":"resource","resource_type":"project","resource_id":"p1"}}`, subject)
		req, _ := http.NewRequest("POST", server.URL+pathAssignRole, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, subject := range []string{"u1", "u2", "u3"} {
		if code := post(subject); code != http.StatusOK {
			t.Fatalf("assign %s: %d", subject, code)
		}
	}
	if code := post("admin-1"); code != http.StatusForbidden {
		t.Fatalf("self-assignment: %d", code)
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		resp, err := http.Get(server.URL + pathAudit + "?limit=2&cursor=" + cursor)
		if err != nil {
			t.Fatal(err)
		}
		var page pageResponse[auditRecordResponse]
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: %d %v", pages, resp.StatusCode, err)
		}
		resp.Body.Close()
		for _, item := range page.Items {
			if seen[item.ID] || item.Change.Tuple.Subject.ID == "admin-1" || item.Source.ActorID != "admin-1" {
				t.Fatalf("unexpected record %+v", item)
			}
			seen[item.ID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 || len(events.got) != 2 || events.got[0].Returned != 2 || events.got[1].Returned != 1 {
		t.Fatalf("records=%d events=%+v", len(seen), events.got)
	}
}

func TestAuditGateDenialsCarryNoStore(t *testing.T) {
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.RespondJSONError(w, web.ErrForbidden("not an auditor"))
		})
	}
	reader, events := &stubAuditReader{}, &auditEvents{}
	a, err := New(Services{Audit: reader}, WithRoleRoutes(RoleRoutes{AuditGate: deny}), WithAuditReadObserver(events.observe))
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	if err := a.Register(h); err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]http.Handler{"Register": h, "Audit()": a.Audit()} {
		rec := doJSON(t, handler, "GET", pathAudit, "")
		if rec.Code != http.StatusForbidden || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d Cache-Control=%q", name, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	if reader.calls != 0 || len(events.got) != 0 {
		t.Fatal("denied read reached the reader or the observer")
	}
}

func TestAuditHeadRecordsNoRead(t *testing.T) {
	reader, events := &stubAuditReader{}, &auditEvents{}
	h := auditRouter(t, reader, adminGate(), events)
	rec := doJSON(t, h, "HEAD", pathAudit, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HEAD status=%d Cache-Control=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if len(events.got) != 0 {
		t.Fatalf("HEAD recorded a read: %+v", events.got)
	}
	doJSON(t, h, "GET", pathAudit, "")
	if len(events.got) != 1 {
		t.Fatalf("GET after HEAD: %d events", len(events.got))
	}
}
