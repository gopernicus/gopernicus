package authenticationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// fakeSecurityEventReader stands in for the service's security-event listing.
type fakeSecurityEventReader struct {
	mu      sync.Mutex
	filters []securityevent.ListFilter
	reqs    []list.Request
	page    list.Page[securityevent.SecurityEvent]
	err     error
}

func (f *fakeSecurityEventReader) SecurityEventsEnabled() bool { return true }

func (f *fakeSecurityEventReader) ListSecurityEvents(_ context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	f.reqs = append(f.reqs, req)
	return f.page, f.err
}

// securityEventsFixture mounts the admin surface behind a recording check
// that denies when deny is set, and the listing when reader is non-nil.
type securityEventsFixture struct {
	*eraseRouteFixture
	deny bool
}

func newSecurityEventsFixture(t *testing.T, reader securityEventReader) *securityEventsFixture {
	t.Helper()
	base := newEraseRouteFixture(t, nil)
	f := &securityEventsFixture{eraseRouteFixture: base}
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: base.svc, ListStrategy: list.StrategyOffset, UserAdminCheck: f.check, SecurityEvents: reader})
	f.h = h
	return f
}

func (f *securityEventsFixture) check(ctx context.Context, req UserAdminCheckRequest) error {
	_ = f.eraseRouteFixture.check(ctx, req)
	if f.deny {
		return fmt.Errorf("not an auditor: %w", sdk.ErrForbidden)
	}
	return nil
}

func (f *securityEventsFixture) list(t *testing.T, key, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/auth/admin/security-events"+query, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := serve(f.h, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestSecurityEventsRouteAbsentWithoutTheOptIn(t *testing.T) {
	f := newSecurityEventsFixture(t, nil)
	key, _ := f.operatorKey(t)
	if rec, _ := f.list(t, key, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("listing without the opt-in = %d, want 404 (not mounted)", rec.Code)
	}
	if len(f.checks) != 0 {
		t.Errorf("an unmounted route consulted the host check: %+v", f.checks)
	}
}

func TestSecurityEventsRouteListsThroughTheReadAuditCheck(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 123456789, time.FixedZone("x", 3600))
	reader := &fakeSecurityEventReader{page: list.Page[securityevent.SecurityEvent]{
		Items: []securityevent.SecurityEvent{{
			ID: "ev-1", UserID: "u2", Actor: securityevent.Principal{Type: "user", ID: "admin"},
			EventType: securityevent.TypeInvitationGranted, EventStatus: securityevent.StatusSuccess,
			Details: map[string]any{"resolved_by": "verification"}, IPAddress: "192.0.2.1", UserAgent: "ua",
			RequestID: "req-1", CreatedAt: at,
		}},
		NextCursor: "next", HasMore: true,
	}}
	f := newSecurityEventsFixture(t, reader)
	key, saID := f.operatorKey(t)
	rec, body := f.list(t, key, "?user_id=u2&event_type=invitation_granted&event_status=success&since=2026-10-25T00:00:00Z&until=2026-10-26T00:00:00.5Z&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("listing = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	if len(f.checks) != 1 || f.checks[0].Action != UserAdminReadAudit || f.checks[0].TargetUserID != "u2" || f.checks[0].Principal.ID != saID {
		t.Fatalf("host checks = %+v, want one read-audit check targeting u2", f.checks)
	}
	want := securityevent.ListFilter{UserID: "u2", EventType: "invitation_granted", EventStatus: "success",
		Since: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 26, 0, 0, 0, 5e8, time.UTC)}
	got := reader.filters[0]
	if got.UserID != want.UserID || got.EventType != want.EventType || got.EventStatus != want.EventStatus || !got.Since.Equal(want.Since) || !got.Until.Equal(want.Until) {
		t.Errorf("filter = %+v, want %+v", got, want)
	}
	if r := reader.reqs[0]; r.Limit != 10 || r.ResolvedStrategy() != list.StrategyCursor || r.Order != securityevent.DefaultOrder {
		t.Errorf("list request = %+v, want cursor, limit 10, created_at DESC", r)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 || body["next_cursor"] != "next" || body["has_more"] != true {
		t.Fatalf("page = %v", body)
	}
	item := items[0].(map[string]any)
	if item["id"] != "ev-1" || item["user_id"] != "u2" || item["actor_type"] != "user" || item["actor_id"] != "admin" ||
		item["event_type"] != "invitation_granted" || item["event_status"] != "success" || item["ip_address"] != "192.0.2.1" ||
		item["request_id"] != "req-1" || item["created_at"] != "2026-10-25T11:00:00.123456789Z" {
		t.Errorf("item = %v", item)
	}
	if _, ok := item["trace_id"]; ok {
		t.Errorf("an empty trace_id is not omitted: %v", item)
	}
	if d, _ := item["details"].(map[string]any); d["resolved_by"] != "verification" {
		t.Errorf("details = %v", item["details"])
	}
}

func TestSecurityEventsRouteRefusesBadQueries(t *testing.T) {
	for _, tc := range []struct{ name, query, code string }{
		{"offset", "?offset=5", codeOffsetNotSupported},
		{"search", "?q=x", ""},
		{"order", "?order=created_at:asc", ""},
		{"limit", "?limit=101", ""},
		{"bad since", "?since=yesterday", ""},
		{"since after until", "?since=2026-10-26T00:00:00Z&until=2026-10-25T00:00:00Z", ""},
		{"since equals until", "?since=2026-10-25T00:00:00Z&until=2026-10-25T00:00:00Z", ""},
		{"repeated key", "?event_type=a&event_type=b", ""},
		{"malformed event_type", "?event_type=Not%20A%20Type", ""},
		{"unknown event_status", "?event_status=maybe", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeSecurityEventReader{}
			f := newSecurityEventsFixture(t, reader)
			key, _ := f.operatorKey(t)
			rec, body := f.list(t, key, tc.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d, want 400; body=%s", tc.query, rec.Code, rec.Body)
			}
			if tc.code != "" && body["code"] != tc.code {
				t.Errorf("code = %v, want %s", body["code"], tc.code)
			}
			if len(reader.filters) != 0 {
				t.Errorf("a refused query reached the reader")
			}
		})
	}
}

func TestSecurityEventsRouteDenialReadsNothing(t *testing.T) {
	reader := &fakeSecurityEventReader{}
	f := newSecurityEventsFixture(t, reader)
	f.deny = true
	key, _ := f.operatorKey(t)
	for _, query := range []string{"?offset=5", "?event_type=a&event_type=b", "?event_status=maybe"} {
		if rec, _ := f.list(t, key, query); rec.Code != http.StatusForbidden {
			t.Fatalf("denied listing %s = %d, want 403 before any query validation", query, rec.Code)
		}
	}
	if rec, _ := f.list(t, key, "?user_id=a&user_id=b"); rec.Code != http.StatusBadRequest {
		t.Fatalf("repeated user_id = %d, want 400 before the host check", rec.Code)
	}
	if len(reader.filters) != 0 {
		t.Error("a denied caller reached the reader")
	}
}

func TestSecurityEventsRouteMapsUnavailable(t *testing.T) {
	f := newSecurityEventsFixture(t, &fakeSecurityEventReader{err: sdk.ErrUnavailable})
	key, _ := f.operatorKey(t)
	if rec, _ := f.list(t, key, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable reader = %d, want 503", rec.Code)
	}
}

func TestSecurityEventsRouteOptionRefusesContradictoryWiring(t *testing.T) {
	f := newEraseRouteFixture(t, nil)
	allow := func(context.Context, UserAdminCheckRequest) error { return nil }
	for name, opts := range map[string][]Option{
		"no check":             {WithSecurityEventsRoute(true)},
		"repository not wired": {WithSecurityEventsRoute(true), WithUserAdminCheck(allow)},
	} {
		if _, err := New(f.svc.Service, environment.ModeDevelopment, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := New(f.svc.Service, environment.ModeDevelopment, WithSecurityEventsRoute(false), WithUserAdminCheck(allow)); err != nil {
		t.Errorf("the opt-in off: %v", err)
	}
}
