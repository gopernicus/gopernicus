package authenticationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// fakeEraser stands in for the service's erasure, recording each call.
type fakeEraser struct {
	mu      sync.Mutex
	calls   []string
	actors  []authlogic.Principal
	erasure user.Erasure
	err     error
}

func (f *fakeEraser) UserErasureEnabled() bool { return true }

func (f *fakeEraser) EraseUser(_ context.Context, actor authlogic.Principal, id string) (user.Summary, user.Erasure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	f.actors = append(f.actors, actor)
	if f.err != nil {
		return user.Summary{}, user.Erasure{}, f.err
	}
	return user.Summary{ID: id, Status: user.StatusErased}, f.erasure, nil
}

// eraseRouteFixture mounts the admin surface with an allow-all, recording
// UserAdminCheck and, when eraser is non-nil, the erase route.
type eraseRouteFixture struct {
	h      http.Handler
	svc    *testService
	mu     sync.Mutex
	checks []UserAdminCheckRequest
}

func newEraseRouteFixture(t *testing.T, eraser userEraser) *eraseRouteFixture {
	t.Helper()
	users := newMemUsers()
	f := &eraseRouteFixture{}
	f.svc = newServiceWithFakes(authenticationFixture{
		Users:           users,
		Identifiers:     newMemIdentifiers(users),
		Passwords:       &memPasswords{m: map[string]string{}},
		Sessions:        &memSessions{m: map[string]session.Session{}},
		Hasher:          fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
		UserAdmin:       memUserAdmin{},
		TokenSigner:     newFakeSigner(),
	})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: f.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: f.check, Eraser: eraser})
	f.h = h
	return f
}

func (f *eraseRouteFixture) check(_ context.Context, req UserAdminCheckRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, req)
	return nil
}

// operatorKey mints a machine credential the admin surface admits (the host's
// check decides), so the case needs no browser session.
func (f *eraseRouteFixture) operatorKey(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	sa, err := f.svc.CreateServiceAccount(ctx, "admin", "operator", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return raw, sa.ID
}

func (f *eraseRouteFixture) erase(t *testing.T, key, target string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/auth/admin/users/"+target+"/erase", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := serve(f.h, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestEraseRouteAbsentWithoutTheOptIn(t *testing.T) {
	f := newEraseRouteFixture(t, nil)
	key, _ := f.operatorKey(t)
	if code, _ := f.erase(t, key, "target"); code != http.StatusNotFound {
		t.Fatalf("erase without the opt-in = %d, want 404 (not mounted)", code)
	}
	if len(f.checks) != 0 {
		t.Errorf("an unmounted route consulted the host check: %+v", f.checks)
	}
}

func TestEraseRouteMountedWithTheOptIn(t *testing.T) {
	eraser := &fakeEraser{erasure: user.Erasure{Changed: true, Counts: user.EraseCounts{Sessions: 2, APIKeys: 1}}}
	f := newEraseRouteFixture(t, eraser)
	key, saID := f.operatorKey(t)
	code, body := f.erase(t, key, "target")
	if code != http.StatusOK {
		t.Fatalf("erase = %d, want 200; body=%v", code, body)
	}
	if len(f.checks) != 1 || f.checks[0].Action != UserAdminErase || f.checks[0].TargetUserID != "target" || f.checks[0].Principal.ID != saID {
		t.Fatalf("host checks = %+v, want one erase check on target", f.checks)
	}
	if len(eraser.calls) != 1 || eraser.calls[0] != "target" || eraser.actors[0].ID != saID {
		t.Fatalf("eraser calls = %v actors = %v", eraser.calls, eraser.actors)
	}
	u, _ := body["user"].(map[string]any)
	counts, _ := body["counts"].(map[string]any)
	if u["status"] != "erased" || body["changed"] != true || counts["sessions"] != float64(2) || counts["api_keys"] != float64(1) || len(counts) != 17 {
		t.Errorf("body = %v", body)
	}
}

func TestEraseRouteMapsRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{user.ErrEraseBlockedByClaim, http.StatusConflict},
		{authlogic.ErrEraseSelf, http.StatusBadRequest},
		{sdk.ErrNotFound, http.StatusNotFound},
	} {
		f := newEraseRouteFixture(t, &fakeEraser{err: tc.err})
		key, _ := f.operatorKey(t)
		if code, body := f.erase(t, key, "target"); code != tc.want {
			t.Errorf("%v: status = %d, want %d; body=%v", tc.err, code, tc.want, body)
		}
	}
}

func TestEraseRouteOptionRefusesContradictoryWiring(t *testing.T) {
	f := newEraseRouteFixture(t, nil)
	allow := func(context.Context, UserAdminCheckRequest) error { return nil }
	for name, opts := range map[string][]Option{
		"no check":          {WithUserErasureRoute(true)},
		"erasure not wired": {WithUserErasureRoute(true), WithUserAdminCheck(allow)},
	} {
		if _, err := New(f.svc.Service, environment.ModeDevelopment, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := New(f.svc.Service, environment.ModeDevelopment, WithUserErasureRoute(false), WithUserAdminCheck(allow)); err != nil {
		t.Errorf("the opt-in off: %v", err)
	}
}
