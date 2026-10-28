package authentication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

const hookSecretText = "hook-private-detail"

type hookCtxKey struct{}

// eraseHookCall is one BeforeErase invocation and what it saw.
type eraseHookCall struct {
	actor       sdk.Principal
	userID      string
	requestVal  any
	deadline    time.Time
	hasDeadline bool
	storeCalls  int
}

// recordingHook records each call and answers from errs in order, then nil.
type recordingHook struct {
	mu    sync.Mutex
	store *eraseStore
	calls []eraseHookCall
	errs  []error
	// honourCancel returns ctx.Err() when the context is already done.
	honourCancel bool
}

func (h *recordingHook) hook(ctx context.Context, actor sdk.Principal, userID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	deadline, ok := ctx.Deadline()
	h.store.mu.Lock()
	storeCalls := len(h.store.inputs)
	h.store.mu.Unlock()
	h.calls = append(h.calls, eraseHookCall{actor: actor, userID: userID, requestVal: ctx.Value(hookCtxKey{}), deadline: deadline, hasDeadline: ok, storeCalls: storeCalls})
	if h.honourCancel && ctx.Err() != nil {
		return ctx.Err()
	}
	if len(h.errs) > 0 {
		err := h.errs[0]
		h.errs = h.errs[1:]
		return err
	}
	return nil
}

func newHookedEraseService(store *eraseStore, hook EraseHook, events *eraseEvents, log *bytes.Buffer) *Service {
	recorder := securityevent.NewRecorder(events, sdk.IDGenerator{}, nil, nil, securityevent.WithClientInfoReader(ClientInfoFromContext))
	var erasure user.EraseRepository
	if store != nil {
		erasure = store
	}
	return newService(constructorConfig{
		Identifiers: eraseIdentifiers{byUser: map[string][]identifier.Identifier{
			"target": {{ID: "i1", UserID: "target", Kind: identifier.KindEmail, NormalizedValue: "target@example.com"}},
		}},
		UserAdmin:             &eraseAdmin{},
		UserErasure:           erasure,
		BeforeErase:           hook,
		SecurityEventRecorder: recorder,
		Logger:                slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
}

func TestEraseUserWithoutHookErases(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{result: user.Erasure{Changed: true}}
	svc := newHookedEraseService(store, nil, &eraseEvents{}, &log)
	if _, erasure, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); err != nil || !erasure.Changed {
		t.Fatalf("EraseUser = %+v, %v", erasure, err)
	}
	if len(store.inputs) != 1 {
		t.Fatalf("store calls = %d, want 1", len(store.inputs))
	}
	store.err = user.ErrEraseBlockedByClaim
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, user.ErrEraseBlockedByClaim) {
		t.Fatalf("claim refusal = %v", err)
	}
	if log.Len() != 0 {
		t.Errorf("no hook, yet the service logged: %s", log.String())
	}
}

func TestEraseUserHookRefusalErasesNothing(t *testing.T) {
	var log bytes.Buffer
	events := &eraseEvents{}
	store := &eraseStore{result: user.Erasure{Changed: true}}
	hostErr := fmt.Errorf("last platform admin %s: %w", hookSecretText, sdk.ErrConflict)
	rec := &recordingHook{store: store, errs: []error{hostErr}}
	svc := newHookedEraseService(store, rec.hook, events, &log)

	_, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target")
	if !errors.Is(err, ErrEraseRefusedByHook) || !errors.Is(err, sdk.ErrConflict) || !errors.Is(err, hostErr) {
		t.Fatalf("err = %v, want ErrEraseRefusedByHook wrapping the host's conflict", err)
	}
	if len(store.inputs) != 0 || len(events.recorded()) != 0 {
		t.Fatalf("a hook refusal reached the store (%d) or the rail (%d)", len(store.inputs), len(events.recorded()))
	}
	out := log.String()
	if strings.Count(out, "auth: erase refused by BeforeErase") != 1 {
		t.Fatalf("want one refusal WARN, got: %s", out)
	}
	for _, want := range []string{"level=WARN", "user_id=target", "actor_id=admin-1", "error_kind=conflict"} {
		if !strings.Contains(out, want) {
			t.Errorf("WARN lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, hookSecretText) || strings.Contains(out, "last platform admin") {
		t.Errorf("the WARN carries the hook's text: %s", out)
	}
}

func TestEraseUserHookNotCalledWhenThePocketRefuses(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{}
	rec := &recordingHook{store: store}
	svc := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)
	if _, _, err := svc.EraseUser(context.Background(), Principal{Type: PrincipalUser, ID: "target"}, "target"); !errors.Is(err, ErrEraseSelf) {
		t.Errorf("self: err = %v", err)
	}
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, " "); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty id: err = %v", err)
	}
	unwired := newHookedEraseService(nil, rec.hook, &eraseEvents{}, &log)
	if _, _, err := unwired.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, ErrUserErasureUnavailable) {
		t.Errorf("missing port: err = %v", err)
	}
	noDirectory := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)
	noDirectory.userAdmin = nil
	if _, _, err := noDirectory.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, ErrUserAdminUnavailable) {
		t.Errorf("missing directory: err = %v", err)
	}
	if len(rec.calls) != 0 || len(store.inputs) != 0 {
		t.Fatalf("hook calls = %d, store calls = %d, want none", len(rec.calls), len(store.inputs))
	}
}

func TestEraseUserHookRunsOnceBeforeTheStore(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{result: user.Erasure{Changed: true}}
	rec := &recordingHook{store: store}
	svc := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)

	ctx := context.WithValue(context.Background(), hookCtxKey{}, "req-42")
	if _, erasure, err := svc.EraseUser(ctx, eraseAdminActor, "target"); err != nil || !erasure.Changed {
		t.Fatalf("EraseUser = %+v, %v", erasure, err)
	}
	finished := time.Now()
	if len(rec.calls) != 1 || len(store.inputs) != 1 {
		t.Fatalf("hook calls = %d, store calls = %d, want 1 and 1", len(rec.calls), len(store.inputs))
	}
	call := rec.calls[0]
	if call.storeCalls != 0 {
		t.Errorf("the store ran before the hook (%d calls)", call.storeCalls)
	}
	if call.actor != eraseAdminActor || call.userID != "target" {
		t.Errorf("hook saw actor=%+v id=%q", call.actor, call.userID)
	}
	if call.requestVal != "req-42" {
		t.Errorf("the hook context lost the request's values: %v", call.requestVal)
	}
	if !call.hasDeadline || call.deadline.After(finished.Add(EraseTimeout)) {
		t.Errorf("hook deadline = %v (set=%v), want within EraseTimeout of the call", call.deadline, call.hasDeadline)
	}

	// Repeat: the hook runs again (it is the orphan sweep); the store answers
	// Changed:false.
	store.result = user.Erasure{Changed: false}
	if _, erasure, err := svc.EraseUser(ctx, eraseAdminActor, "target"); err != nil || erasure.Changed {
		t.Fatalf("repeat = %+v, %v", erasure, err)
	}
	if len(rec.calls) != 2 || len(store.inputs) != 2 {
		t.Fatalf("repeat: hook calls = %d, store calls = %d, want 2 and 2", len(rec.calls), len(store.inputs))
	}
}

func TestEraseUserHookSeesACancelledRequest(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{result: user.Erasure{Changed: true}}
	rec := &recordingHook{store: store, honourCancel: true}
	svc := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := svc.EraseUser(ctx, eraseAdminActor, "target"); !errors.Is(err, ErrEraseRefusedByHook) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the hook's cancellation", err)
	}
	if len(rec.calls) != 1 || len(store.inputs) != 0 {
		t.Fatalf("hook calls = %d, store calls = %d, want 1 and 0", len(rec.calls), len(store.inputs))
	}
	if !strings.Contains(log.String(), "error_kind=unknown") {
		t.Errorf("WARN = %s", log.String())
	}
}

func TestEraseUserReRunAfterHookFailureErases(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{result: user.Erasure{Changed: true}}
	rec := &recordingHook{store: store, errs: []error{errors.New("authorization store unreachable")}}
	svc := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, ErrEraseRefusedByHook) {
		t.Fatalf("first: err = %v", err)
	}
	if len(store.inputs) != 0 {
		t.Fatalf("a failed hook reached the store")
	}
	if _, erasure, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); err != nil || !erasure.Changed {
		t.Fatalf("re-run = %+v, %v", erasure, err)
	}
	if len(rec.calls) != 2 || len(store.inputs) != 1 {
		t.Fatalf("hook calls = %d, store calls = %d, want 2 and 1", len(rec.calls), len(store.inputs))
	}
}

func TestEraseUserStoreRefusalAfterHookWarns(t *testing.T) {
	var log bytes.Buffer
	store := &eraseStore{err: user.ErrEraseBlockedByClaim}
	rec := &recordingHook{store: store}
	svc := newHookedEraseService(store, rec.hook, &eraseEvents{}, &log)
	_, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target")
	if !errors.Is(err, user.ErrEraseBlockedByClaim) || errors.Is(err, ErrEraseRefusedByHook) {
		t.Fatalf("err = %v, want the store's claim refusal unwrapped", err)
	}
	if len(rec.calls) != 1 || len(store.inputs) != 1 {
		t.Fatalf("hook calls = %d, store calls = %d, want 1 and 1", len(rec.calls), len(store.inputs))
	}
	out := log.String()
	if strings.Count(out, "auth: erase refused after BeforeErase ran") != 1 {
		t.Fatalf("want one after-hook WARN, got: %s", out)
	}
	for _, want := range []string{"user_id=target", "actor_id=admin-1", "error_kind=conflict"} {
		if !strings.Contains(out, want) {
			t.Errorf("WARN lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "refused by BeforeErase") {
		t.Errorf("a successful hook logged a hook refusal: %s", out)
	}
}
