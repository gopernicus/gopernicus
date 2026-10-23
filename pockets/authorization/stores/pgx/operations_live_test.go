package pgx

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/jackc/pgx/v5"
)

type fenceRaceKey struct{}

// fenceRaceTracer, once armed, runs after the first fence INSERT for id that
// inserted nothing, before Fence reads the conflicting record back.
type fenceRaceTracer struct {
	id     string
	armed  atomic.Bool
	fired  atomic.Int32
	expire func()
}

func (f *fenceRaceTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	fence := strings.HasPrefix(data.SQL, "INSERT INTO ") && strings.Contains(data.SQL, "iam_operations") && len(data.Args) == 5 && data.Args[0] == f.id && data.Args[3] == mutations.OutcomeFenced
	return context.WithValue(ctx, fenceRaceKey{}, fence)
}

func (f *fenceRaceTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if fence, _ := ctx.Value(fenceRaceKey{}).(bool); fence && data.Err == nil && data.CommandTag.RowsAffected() == 0 && f.armed.CompareAndSwap(true, false) {
		f.fired.Add(1)
		f.expire()
	}
}

// TestFenceRecordsFencedWhenExpireCommitsBetweenInsertAndSelect is the
// deterministic Expire/Fence race: Fence's INSERT conflicts with an applied
// record, Expire deletes and commits that record on another connection, and
// Fence's read-back finds nothing. Fence re-runs the INSERT and records the ID
// as fenced instead of failing.
func TestFenceRecordsFencedWhenExpireCommitsBetweenInsertAndSelect(t *testing.T) {
	dsn := requireDSN(t)
	admin := openAndMigrate(t, dsn)
	adminRepos, err := testRepositories(t.Context(), admin, storeOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	tracer := &fenceRaceTracer{id: "invitation:race"}
	var expired int
	var expireErr error
	tracer.expire = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		expired, expireErr = adminRepos.OperationExpiry.Expire(ctx, time.Now().Add(time.Second), 10)
	}
	traced, err := pgxdb.Open(t.Context(), pgxdb.Config{DSN: dsn, Tracer: tracer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { traced.Close() })
	repos, err := testRepositories(t.Context(), traced, storeOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	cmd := grantCmd("race", "viewer", "alice")
	cmd.OperationID = tracer.id
	if got := mustApplyLive(t, repos.Mutations, cmd); got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("apply: %+v", got)
	}

	tracer.armed.Store(true)
	got, err := repos.Operations.Fence(t.Context(), tracer.id)
	if err != nil || got == nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
		t.Fatalf("Fence across a committed Expire: %+v %v", got, err)
	}
	if tracer.fired.Load() != 1 || expireErr != nil || expired != 1 {
		t.Fatalf("the race did not run as staged: fired=%d expired=%d err=%v", tracer.fired.Load(), expired, expireErr)
	}
	if outcome, ok, err := repos.Operations.Recorded(t.Context(), tracer.id); err != nil || !ok || outcome != mutations.OutcomeFenced {
		t.Fatalf("Recorded after the race: %q %v %v", outcome, ok, err)
	}
	if replay := mustApplyLive(t, repos.Mutations, cmd); replay.Outcome != mutations.OutcomeFenced || !replay.Replayed || !replay.Superseded {
		t.Fatalf("Apply after the fence: %+v", replay)
	}
}
