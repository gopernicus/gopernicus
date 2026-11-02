package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// deadlineRevoker is a one-batch GlobalRevoker that records the deadline its
// context carried.
type deadlineRevoker struct {
	deadline time.Time
	ok       bool
}

func (r *deadlineRevoker) RevokeAllBatch(ctx context.Context, _ string, _ int, _ time.Time) (session.GlobalRevocationBatch, error) {
	r.deadline, r.ok = ctx.Deadline()
	return session.GlobalRevocationBatch{Users: 3, Sessions: 4, LastUserID: "u3", Done: true}, nil
}

func (r *deadlineRevoker) SweepOrphans(context.Context, time.Time) (int, int, error) {
	return 0, 0, nil
}

// TestRevokeAllSessionsJobRuns proves a payload built by NewRevokeAllSessionsJob
// runs RevokeAllSessions under the job's ten-minute limit, with the evidence
// attributed to the payload's actor.
func TestRevokeAllSessionsJobRuns(t *testing.T) {
	h := newHarness(t, nil)
	rev := &deadlineRevoker{}
	h.svc.globalSessions = rev
	payload, err := NewRevokeAllSessionsJob(Principal{Type: "system", ID: "incident-cli"})
	if err != nil {
		t.Fatalf("NewRevokeAllSessionsJob: %v", err)
	}
	if string(payload) != `{"actor_type":"system","actor_id":"incident-cli"}` {
		t.Fatalf("payload = %s", payload)
	}
	start := time.Now()
	if err := h.svc.RevokeAllSessionsJobHandler()(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !rev.ok {
		t.Fatal("the run carried no deadline")
	}
	if left := rev.deadline.Sub(start); left < 9*time.Minute || left > 10*time.Minute+time.Second {
		t.Fatalf("deadline %v after start, want about 10m", left)
	}
	rows := globalRows(h.events)
	if len(rows) != 2 || rows[1].EventStatus != securityevent.StatusSuccess || rows[1].Details["users"] != 3 {
		t.Fatalf("rows = %+v, want started then success with the counts", rows)
	}
	for _, r := range rows {
		if r.Actor.Type != "system" || r.Actor.ID != "incident-cli" {
			t.Errorf("row actor = %+v, want the payload's actor", r.Actor)
		}
	}
}

// TestRevokeAllSessionsJobRejectsBadPayloads proves a payload that cannot name
// an actor is refused before any run or evidence row.
func TestRevokeAllSessionsJobRejectsBadPayloads(t *testing.T) {
	cases := map[string]string{
		"empty":         ``,
		"not json":      `revoke`,
		"no actor":      `{}`,
		"no actor id":   `{"actor_type":"user"}`,
		"no actor type": `{"actor_id":"op"}`,
		"unknown field": `{"actor_type":"user","actor_id":"op","all":true}`,
		"trailing":      `{"actor_type":"user","actor_id":"op"}{}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			rev := &deadlineRevoker{}
			h.svc.globalSessions = rev
			err := h.svc.RevokeAllSessionsJobHandler()(context.Background(), []byte(payload))
			if !errors.Is(err, ErrRevokeAllSessionsJobInvalid) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrRevokeAllSessionsJobInvalid", err)
			}
			if rev.ok {
				t.Fatal("a bad payload reached the store")
			}
			if rows := globalRows(h.events); len(rows) != 0 {
				t.Fatalf("a bad payload recorded %+v", rows)
			}
		})
	}
}

func TestNewRevokeAllSessionsJobRequiresActor(t *testing.T) {
	for _, actor := range []Principal{{}, {Type: "user"}, {ID: "op"}} {
		if _, err := NewRevokeAllSessionsJob(actor); !errors.Is(err, ErrRevokeAllSessionsJobInvalid) {
			t.Errorf("NewRevokeAllSessionsJob(%+v) = %v, want ErrRevokeAllSessionsJobInvalid", actor, err)
		}
	}
}

func TestRevokeAllSessionsJobUnwired(t *testing.T) {
	h := newHarness(t, nil)
	payload, _ := NewRevokeAllSessionsJob(Principal{Type: PrincipalUser, ID: "op"})
	if err := h.svc.RevokeAllSessionsJobHandler()(context.Background(), payload); !errors.Is(err, ErrGlobalRevocationUnsupported) {
		t.Fatalf("unwired = %v, want ErrGlobalRevocationUnsupported", err)
	}
}
