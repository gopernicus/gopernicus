package authenticationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// ruleRepo is an in-memory InvitationRepository covering the create, read, list,
// and management transitions the resource-rule routes drive.
type ruleRepo struct {
	invitations.InvitationRepository
	mu     sync.Mutex
	rows   map[string]invitations.Invitation
	order  []string
	writes int
	lists  int
}

func newRuleRepo() *ruleRepo { return &ruleRepo{rows: map[string]invitations.Invitation{}} }

func (r *ruleRepo) Create(_ context.Context, in invitations.Invitation) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.ID == "" {
		in.ID = fmt.Sprintf("inv-%d", len(r.order)+1)
	}
	r.rows[in.ID] = in
	r.order = append(r.order, in.ID)
	r.writes++
	return in, nil
}
func (r *ruleRepo) Get(_ context.Context, id string) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	return row, nil
}
func (r *ruleRepo) ListByResource(_ context.Context, resourceType, resourceID string, _ list.Request) (list.Page[invitations.Invitation], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	var items []invitations.Invitation
	for _, id := range r.order {
		if row := r.rows[id]; row.ResourceType == resourceType && row.ResourceID == resourceID {
			items = append(items, row)
		}
	}
	return list.Page[invitations.Invitation]{Items: items}, nil
}
func (r *ruleRepo) UpdateStatus(_ context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok {
		return invitations.Invitation{}, sdk.ErrNotFound
	}
	if upd.ExpectedTokenHash != row.TokenHash {
		return invitations.Invitation{}, sdk.ErrConflict
	}
	row.Status, row.TokenHash, row.ExpiresAt = upd.Status, upd.TokenHash, upd.ExpiresAt
	r.rows[id] = row
	r.writes++
	return row, nil
}
func (r *ruleRepo) seed(inv invitations.Invitation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[inv.ID] = inv
	r.order = append(r.order, inv.ID)
}
func (r *ruleRepo) row(id string) invitations.Invitation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows[id]
}

// countingQueue counts every delivery the service queues.
type countingQueue struct {
	stubQueue
	mu         sync.Mutex
	deliveries int
}

func (q *countingQueue) Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.mu.Lock()
	q.deliveries++
	q.mu.Unlock()
	return q.stubQueue.Enqueue(ctx, cmd)
}
func (q *countingQueue) Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error) {
	q.mu.Lock()
	q.deliveries++
	q.mu.Unlock()
	return q.stubQueue.Replace(ctx, cmd)
}

type ruleEvents struct {
	mu      sync.Mutex
	created []securityevent.SecurityEvent
}

func (e *ruleEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.created = append(e.created, evt)
	return evt, nil
}
func (e *ruleEvents) List(context.Context, securityevent.ListFilter, list.Request) (list.Page[securityevent.SecurityEvent], error) {
	return list.Page[securityevent.SecurityEvent]{}, nil
}
func (e *ruleEvents) last() securityevent.SecurityEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.created[len(e.created)-1]
}

// ruleWorld is a real invitation service behind the HTTP routes, with a host
// authorizer whose holders of "manage" on project/p1 are mutable per test.
type ruleWorld struct {
	invitationFixture
	repo     *ruleRepo
	granter  *policyGranter
	queue    *countingQueue
	events   *ruleEvents
	mu       sync.Mutex
	holders  map[string]bool
	canErr   error
	canCalls int
	trace    []string
}

func (w *ruleWorld) can(_ context.Context, p sdk.Principal, permission, resourceType, resourceID string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.canCalls++
	w.trace = append(w.trace, "can")
	if w.canErr != nil {
		return false, w.canErr
	}
	if p.Type != sdk.PrincipalTypeUser || permission != "manage" || resourceType != "project" || resourceID != "p1" {
		return false, nil
	}
	return w.holders[p.ID], nil
}

func (w *ruleWorld) revoke(userID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.holders, userID)
}

// newRuleWorld mounts the invitation routes with the resource rule ON and an
// optional InviteCheck refinement (nil → rule-only authority).
func newRuleWorld(t *testing.T, check InviteCheck) *ruleWorld {
	t.Helper()
	w := &ruleWorld{repo: newRuleRepo(), granter: &policyGranter{}, queue: &countingQueue{}, events: &ruleEvents{}, holders: map[string]bool{"u1": true, "u2": true}}
	router, err := delivery.NewRouter(nopMailer{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := invitations.New(w.repo, w.granter,
		invitations.WithAccess(invitations.AccessConfig{UserLookup: func(_ context.Context, address string) (string, bool, error) {
			if address == "known@x.com" {
				return "known-user", true, nil
			}
			return "", false, nil
		}}),
		invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}, Deliver: router, Queue: w.queue}),
		invitations.WithSecurityEvents(w.events))
	if err != nil {
		t.Fatal(err)
	}
	var wrapped InviteCheck
	if check != nil {
		wrapped = func(ctx context.Context, req InviteCheckRequest) error {
			w.mu.Lock()
			w.trace = append(w.trace, "invite-check:"+string(req.Action))
			w.mu.Unlock()
			return check(ctx, req)
		}
	}
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	auth := newServiceWithFakes(authenticationFixture{Users: users, Identifiers: idents, Passwords: passwords, Sessions: sessions, Hasher: fakeHasher{}, Limiter: ratelimiter.NewMemory(), TokenSigner: newFakeSigner()})
	h := web.NewWebHandler()
	mount(h, mountDeps{Auth: auth, Invitations: svc, InviteCheck: wrapped, ResourceRule: InvitationResourceRule{Permissions: map[string]string{"project": "manage"}, Can: w.can}, ListStrategy: list.StrategyCursor})
	w.invitationFixture = invitationFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions}
	for id, address := range map[string]string{"u1": "alice@x.com", "u2": "bob@x.com", "u3": "mallory@x.com"} {
		w.seedRuleUser(id, address)
	}
	return w
}

// seedRuleUser is seedLoginUser with a per-user identifier ID, so several
// managers can log in to one fixture.
func (w *ruleWorld) seedRuleUser(userID, address string) {
	w.users.mu.Lock()
	w.users.byID[userID] = user.User{ID: userID, DisplayName: "Seed"}
	w.users.mu.Unlock()
	w.passwords.mu.Lock()
	w.passwords.m[userID] = "hash:password123456789"
	w.passwords.mu.Unlock()
	w.idents.insert(verifiedEmail("id-"+userID, userID, address))
}

func (w *ruleWorld) seedPending(id, resourceType, resourceID, invitedBy string) {
	w.repo.seed(invitations.Invitation{ID: id, ResourceType: resourceType, ResourceID: resourceID, Relation: "member", Identifier: "invitee@x.com", IdentifierKind: "email", InvitedBy: invitedBy, TokenHash: "initial", Status: invitations.StatusPending, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()})
}

func (w *ruleWorld) resetTrace() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.trace, w.canCalls = nil, 0
}

type ruleEffects struct{ writes, lists, grants, deliveries int }

func (w *ruleWorld) effects() ruleEffects {
	w.queue.mu.Lock()
	defer w.queue.mu.Unlock()
	return ruleEffects{writes: w.repo.writes, lists: w.repo.lists, grants: len(w.granter.calls), deliveries: w.queue.deliveries}
}

func TestInvitationResourceRuleCoManagerAdministersAll(t *testing.T) {
	w := newRuleWorld(t, nil)
	alice, bob := w.login(t, "alice@x.com"), w.login(t, "bob@x.com")

	rec := do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"invitee@x.com","relation":"member"}`, alice)
	if rec.Code != http.StatusCreated {
		t.Fatalf("A create=%d body=%s", rec.Code, rec.Body)
	}
	var created invitationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.InvitedBy != "u1" {
		t.Fatalf("created=%+v", created)
	}
	if rec := do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"other@x.com","relation":"member"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("B create=%d body=%s", rec.Code, rec.Body)
	}
	rec = do(t, w.h, "GET", "/auth/invitations/project/p1", "", bob)
	if rec.Code != http.StatusOK {
		t.Fatalf("B list=%d body=%s", rec.Code, rec.Body)
	}
	var page struct {
		Items []invitationResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("B list page=%+v err=%v body=%s", page, err, rec.Body)
	}

	before := w.repo.row(created.ID).TokenHash
	if rec := do(t, w.h, "POST", "/auth/invitations/"+created.ID+"/resend", "", bob); rec.Code != http.StatusOK {
		t.Fatalf("B resend A's invitation=%d body=%s", rec.Code, rec.Body)
	}
	if w.repo.row(created.ID).TokenHash == before {
		t.Fatal("resend did not rotate the token")
	}
	evt := w.events.last()
	if evt.EventType != securityevent.TypeInvitationCreated || evt.Actor != (securityevent.Principal{Type: sdk.PrincipalTypeUser, ID: "u2"}) || evt.UserID != "u2" || evt.Details["invited_by"] != "u1" {
		t.Fatalf("resend audit=%+v", evt)
	}
	if rec := do(t, w.h, "POST", "/auth/invitations/"+created.ID+"/cancel", "", bob); rec.Code != http.StatusOK {
		t.Fatalf("B cancel A's invitation=%d body=%s", rec.Code, rec.Body)
	}
	if w.repo.row(created.ID).Status != invitations.StatusCancelled {
		t.Fatalf("status=%s", w.repo.row(created.ID).Status)
	}
	evt = w.events.last()
	if evt.EventType != securityevent.TypeInvitationCancelled || evt.Actor.ID != "u2" || evt.UserID != "u2" || evt.Details["invited_by"] != "u1" {
		t.Fatalf("cancel audit=%+v", evt)
	}
}

// ruleOps drives create, list, resend, and cancel against project/p1 (or the
// given resource type) and returns each status.
func ruleOps(t *testing.T, w *ruleWorld, cookie *http.Cookie, resourceType, id string) map[string]int {
	t.Helper()
	return map[string]int{
		"create": do(t, w.h, "POST", "/auth/invitations/"+resourceType+"/p1", `{"identifier":"new@x.com","relation":"member"}`, cookie).Code,
		"list":   do(t, w.h, "GET", "/auth/invitations/"+resourceType+"/p1", "", cookie).Code,
		"resend": do(t, w.h, "POST", "/auth/invitations/"+id+"/resend", "", cookie).Code,
		"cancel": do(t, w.h, "POST", "/auth/invitations/"+id+"/cancel", "", cookie).Code,
	}
}

func TestInvitationResourceRuleRefusals(t *testing.T) {
	want := map[string]int{"create": http.StatusForbidden, "list": http.StatusForbidden, "resend": http.StatusNotFound, "cancel": http.StatusNotFound}
	t.Run("non-holder", func(t *testing.T) {
		w := newRuleWorld(t, nil)
		w.seedPending("inv-a", "project", "p1", "u1")
		base := w.effects()
		got := ruleOps(t, w, w.login(t, "mallory@x.com"), "project", "inv-a")
		if !maps.Equal(got, want) {
			t.Fatalf("statuses=%v want %v", got, want)
		}
		if w.effects() != base || w.repo.row("inv-a").TokenHash != "initial" || w.repo.row("inv-a").Status != invitations.StatusPending {
			t.Fatalf("refused effects=%+v row=%+v", w.effects(), w.repo.row("inv-a"))
		}
	})
	t.Run("inviter lost permission", func(t *testing.T) {
		w := newRuleWorld(t, nil)
		w.seedPending("inv-a", "project", "p1", "u1")
		w.revoke("u1")
		alice := w.login(t, "alice@x.com")
		for _, op := range []string{"resend", "cancel"} {
			if rec := do(t, w.h, "POST", "/auth/invitations/inv-a/"+op, "", alice); rec.Code != http.StatusNotFound {
				t.Fatalf("revoked inviter %s=%d body=%s", op, rec.Code, rec.Body)
			}
		}
		if w.repo.writes != 0 || w.repo.row("inv-a").TokenHash != "initial" {
			t.Fatal("revoked inviter changed state")
		}
	})
	t.Run("unmapped type", func(t *testing.T) {
		w := newRuleWorld(t, nil)
		w.seedPending("inv-f", "folder", "p1", "u1")
		base := w.effects()
		got := ruleOps(t, w, w.login(t, "alice@x.com"), "folder", "inv-f")
		if !maps.Equal(got, want) || w.canCalls != 0 || w.effects() != base {
			t.Fatalf("statuses=%v canCalls=%d effects=%+v", got, w.canCalls, w.effects())
		}
	})
	t.Run("unknown id is indistinguishable", func(t *testing.T) {
		w := newRuleWorld(t, nil)
		rec := do(t, w.h, "POST", "/auth/invitations/missing/cancel", "", w.login(t, "mallory@x.com"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown id=%d", rec.Code)
		}
	})
}

func TestInvitationResourceRuleCanErrorFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"infrastructure", errors.New("authorizer down"), http.StatusInternalServerError},
		{"not found", fmt.Errorf("no such project: %w", sdk.ErrNotFound), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRuleWorld(t, nil)
			w.seedPending("inv-a", "project", "p1", "u1")
			w.canErr = tc.err
			base := w.effects()
			got := ruleOps(t, w, w.login(t, "alice@x.com"), "project", "inv-a")
			for op, code := range got {
				if code != tc.want {
					t.Fatalf("%s=%d want %d", op, code, tc.want)
				}
			}
			if w.effects() != base || w.repo.row("inv-a").TokenHash != "initial" {
				t.Fatalf("Can error changed state: %+v", w.effects())
			}
		})
	}
}

func TestInvitationResourceRuleInviteCheckIsCreateOnlyRefinement(t *testing.T) {
	w := newRuleWorld(t, func(context.Context, InviteCheckRequest) error { return nil })
	w.seedPending("inv-a", "project", "p1", "u1")
	bob := w.login(t, "bob@x.com")
	w.resetTrace()
	if rec := do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"new@x.com","relation":"member"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", rec.Code, rec.Body)
	}
	if !slices.Equal(w.trace, []string{"can", "invite-check:create"}) {
		t.Fatalf("create trace=%v", w.trace)
	}
	for _, step := range []struct{ method, path string }{{"GET", "/auth/invitations/project/p1"}, {"POST", "/auth/invitations/inv-a/resend"}, {"POST", "/auth/invitations/inv-a/cancel"}} {
		w.resetTrace()
		if rec := do(t, w.h, step.method, step.path, "", bob); rec.Code != http.StatusOK {
			t.Fatalf("%s=%d body=%s", step.path, rec.Code, rec.Body)
		}
		if !slices.Equal(w.trace, []string{"can"}) {
			t.Fatalf("%s trace=%v", step.path, w.trace)
		}
	}
	w.resetTrace()
	if rec := do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"new2@x.com","relation":"member"}`, w.login(t, "mallory@x.com")); rec.Code != http.StatusForbidden {
		t.Fatalf("non-holder create=%d", rec.Code)
	}
	if !slices.Equal(w.trace, []string{"can"}) {
		t.Fatalf("InviteCheck ran after Can denied: %v", w.trace)
	}
}

func TestInvitationResourceRuleInviteCheckRestrictsManager(t *testing.T) {
	memberOnly := func(_ context.Context, req InviteCheckRequest) error {
		if req.Relation == "owner" {
			return fmt.Errorf("managers may not invite owners: %w", sdk.ErrForbidden)
		}
		if req.Metadata["route"] == "restricted" {
			return fmt.Errorf("restricted route: %w", sdk.ErrForbidden)
		}
		return nil
	}
	for _, tc := range []struct{ name, body string }{
		{"pending owner", `{"identifier":"new@x.com","relation":"owner"}`},
		{"direct-add owner", `{"identifier":"known@x.com","relation":"owner","auto_accept":true}`},
		{"pending restricted metadata", `{"identifier":"new@x.com","relation":"member","metadata":{"route":"restricted"}}`},
		{"direct-add restricted metadata", `{"identifier":"known@x.com","relation":"member","auto_accept":true,"metadata":{"route":"restricted"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newRuleWorld(t, memberOnly)
			base := w.effects()
			rec := do(t, w.h, "POST", "/auth/invitations/project/p1", tc.body, w.login(t, "bob@x.com"))
			if rec.Code != http.StatusForbidden || w.canCalls != 1 {
				t.Fatalf("status=%d canCalls=%d body=%s", rec.Code, w.canCalls, rec.Body)
			}
			if w.effects() != base {
				t.Fatalf("refused create had effects: %+v", w.effects())
			}
		})
	}
}

// TestInvitationResourceRuleOnlyAuthorityIsBroad pins D1: without InviteCheck the
// mapped permission authorizes any relation the Granter accepts, with metadata.
func TestInvitationResourceRuleOnlyAuthorityIsBroad(t *testing.T) {
	w := newRuleWorld(t, nil)
	bob := w.login(t, "bob@x.com")
	rec := do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"new@x.com","relation":"owner","metadata":{"route":"restricted"}}`, bob)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pending owner=%d body=%s", rec.Code, rec.Body)
	}
	var created invitationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if row := w.repo.row(created.ID); row.Relation != "owner" || row.Metadata["route"] != "restricted" {
		t.Fatalf("row=%+v", row)
	}
	rec = do(t, w.h, "POST", "/auth/invitations/project/p1", `{"identifier":"known@x.com","relation":"owner","auto_accept":true,"metadata":{"route":"restricted"}}`, bob)
	if rec.Code != http.StatusOK {
		t.Fatalf("direct-add owner=%d body=%s", rec.Code, rec.Body)
	}
	if len(w.granter.calls) != 1 || w.granter.calls[0].Relation != "owner" || w.granter.calls[0].SubjectID != "known-user" || w.granter.calls[0].Metadata["route"] != "restricted" {
		t.Fatalf("grants=%+v", w.granter.calls)
	}
}

// TestInvitationRuleOffManagementAttribution pins D5 for the legacy mode: the
// issuer's resend/cancel keep UserID and now carry the canonical Actor.
func TestInvitationRuleOffManagementAttribution(t *testing.T) {
	for _, op := range []string{"resend", "cancel"} {
		t.Run(op, func(t *testing.T) {
			repo := newRuleRepo()
			repo.seed(invitations.Invitation{ID: "inv-a", ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "invitee@x.com", IdentifierKind: "email", InvitedBy: "u1", TokenHash: "initial", Status: invitations.StatusPending, ExpiresAt: time.Now().Add(time.Hour)})
			events := &ruleEvents{}
			router, err := delivery.NewRouter(nopMailer{})
			if err != nil {
				t.Fatal(err)
			}
			inv, err := invitations.New(repo, &policyGranter{}, invitations.WithDelivery(invitations.DeliveryConfig{Mailer: nopMailer{}, Deliver: router, Queue: stubQueue{}}), invitations.WithSecurityEvents(events))
			if err != nil {
				t.Fatal(err)
			}
			f := newInvitationFixtureWith(t, inv, allowInviteCheck, nil)
			f.seedLoginUser("u1", "alice@x.com")
			if rec := do(t, f.h, "POST", "/auth/invitations/inv-a/"+op, "", f.login(t, "alice@x.com")); rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			evt := events.last()
			if evt.Actor != (securityevent.Principal{Type: sdk.PrincipalTypeUser, ID: "u1"}) || evt.UserID != "u1" || evt.Details["invited_by"] != "u1" {
				t.Fatalf("audit=%+v", evt)
			}
		})
	}
}

// TestHTTPConstructorInvitationPolicyMatrix mirrors D2 against service presence:
// every rejection wraps sdk.ErrInvalidInput, incomplete rules first.
func TestHTTPConstructorInvitationPolicyMatrix(t *testing.T) {
	base := newInvitationFixture(t, allowInviteCheck, nil)
	svc := newServiceWithFakes(authenticationFixture{Users: base.users, Identifiers: base.idents, Passwords: base.passwords, Sessions: base.sessions, Hasher: fakeHasher{}, TokenSigner: newFakeSigner()})
	can := func(context.Context, sdk.Principal, string, string, string) (bool, error) { return true, nil }
	perms := map[string]string{"project": "manage"}
	var typedNil *spyInvitationService
	cases := []struct {
		name string
		opts []Option
		ok   bool
	}{
		{"rule on", []Option{WithInvitations(&stubInvitationService{}), WithInvitationResourceRule(InvitationResourceRule{Permissions: perms, Can: can})}, true},
		{"rule on + InviteCheck", []Option{WithInvitations(&stubInvitationService{}), WithInvitationResourceRule(InvitationResourceRule{Permissions: perms, Can: can}), WithInviteCheck(allowInviteCheck)}, true},
		{"rule off + InviteCheck", []Option{WithInvitations(&stubInvitationService{}), WithInviteCheck(allowInviteCheck)}, true},
		{"service without policy", []Option{WithInvitations(&stubInvitationService{})}, false},
		{"lone map", []Option{WithInvitations(&stubInvitationService{}), WithInvitationResourceRule(InvitationResourceRule{Permissions: perms})}, false},
		{"lone Can", []Option{WithInvitations(&stubInvitationService{}), WithInvitationResourceRule(InvitationResourceRule{Can: can})}, false},
		{"invalid entry", []Option{WithInvitations(&stubInvitationService{}), WithInvitationResourceRule(InvitationResourceRule{Permissions: map[string]string{"project": ""}, Can: can})}, false},
		{"rule without service", []Option{WithInvitationResourceRule(InvitationResourceRule{Permissions: perms, Can: can})}, false},
		{"rule with typed-nil service", []Option{WithInvitations(typedNil), WithInvitationResourceRule(InvitationResourceRule{Permissions: perms, Can: can})}, false},
		{"InviteCheck without service", []Option{WithInviteCheck(allowInviteCheck)}, false},
		{"no service, no policy", nil, true},
		{"typed-nil service, no policy", []Option{WithInvitations(typedNil)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(svc.Service, environment.ModeDevelopment, tc.opts...)
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, sdk.ErrInvalidInput)) {
				t.Fatalf("err=%v ok=%v", err, tc.ok)
			}
		})
	}
	// Incomplete precedes absent: a lone map without a service names the rule.
	_, lone := New(svc.Service, environment.ModeDevelopment, WithInvitationResourceRule(InvitationResourceRule{Permissions: perms}))
	_, absent := New(svc.Service, environment.ModeDevelopment, WithInvitationResourceRule(InvitationResourceRule{Permissions: perms, Can: can}))
	if lone == nil || absent == nil || lone.Error() == absent.Error() {
		t.Fatalf("precedence: lone=%v absent=%v", lone, absent)
	}
}
