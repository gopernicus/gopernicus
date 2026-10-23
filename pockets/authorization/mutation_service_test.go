package authorization

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

// stubMutationRepo runs guards through its view, proving the guarded write path
// executes inside the repository (not the outer Service).
type stubMutationRepo struct {
	receipt      *mutations.Result
	applyErr     error
	gotCmd       mutations.Command
	applyTrusted bool
}

func (r *stubMutationRepo) IntegrityPolicy() mutations.IntegrityPolicy {
	return mutations.IntegrityPolicy{}
}

func (r *stubMutationRepo) Apply(_ context.Context, cmd mutations.Command, _ mutations.SemanticValidator) (*mutations.Result, error) {
	r.applyTrusted = true
	r.gotCmd = cmd
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	return r.receipt, nil
}

func validGrantCommand(t *testing.T) mutations.Command {
	t.Helper()
	return mutations.Command{

		Target:        mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutations.OpGrant,
		Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
	}
}

// -----------------------------------------------------------------------------
// Actor
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Guard composition (the AZ3-0.4 seam)
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Construction matrix
// -----------------------------------------------------------------------------

func mustComponents(t *testing.T, repos Repositories, opts ...Option) Components {
	t.Helper()
	comps, err := New(repos, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func TestConstructionReturnsOnlyConfiguredWriteCapability(t *testing.T) {
	readOnly := mustComponents(t, Repositories{Tuples: memory.NewTuples()}, WithModel(validModel()))
	if readOnly.Mutations != nil {
		t.Fatal("missing repository exposed an unusable writer")
	}
	store := memory.New()
	full := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()})
	if full.Mutations == nil {
		t.Fatal("configured writer missing")
	}
}

// TestConstructionMutationsNotConfiguredKind pins the sentinel's sdk kind: the
// not-configured posture is a precondition refusal (sdk.ErrInvalidInput), never
// sdk.ErrUnavailable (saturation) and never sdk.ErrForbidden.
func TestConstructionMutationsNotConfiguredKind(t *testing.T) {
	if !errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrInvalidInput) {
		t.Fatalf("ErrMutationsNotConfigured must wrap sdk.ErrInvalidInput")
	}
	if errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrUnavailable) || errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrForbidden) {
		t.Fatalf("ErrMutationsNotConfigured must not wrap ErrUnavailable/ErrForbidden")
	}
}

// -----------------------------------------------------------------------------
// Audit
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// TeardownResourceAuthorization (AZ3-3.2)
// -----------------------------------------------------------------------------

func teardownCmd(t *testing.T, reason string) mutations.TeardownResourceAuthorizationCommand {
	t.Helper()
	return mutations.TeardownResourceAuthorizationCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Reason:       reason,
	}
}

// TestTeardownReasonRequired proves malformed or empty reasons are refused
// before any write.
func TestTeardownReasonRequired(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Tuples: memory.NewTuples(), Mutations: repo})

	for _, reason := range []string{"", "   ", "bad\x00reason", string([]byte{0xff})} {
		if _, err := comps.Mutations.TeardownResourceAuthorization(context.Background(), teardownCmd(t, reason)); !errors.Is(err, mutations.ErrTeardownReasonRequired) {
			t.Fatalf("reason %q: want ErrTeardownReasonRequired, got %v", reason, err)
		}
	}
	if repo.applyTrusted {
		t.Fatalf("a reasonless teardown must not reach the repository")
	}
}

// TestTeardownReasonTooLong proves an over-bound reason is refused (the audit
// record must stay bounded).
func TestTeardownReasonTooLong(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Tuples: memory.NewTuples(), Mutations: repo})

	long := strings.Repeat("x", mutations.MaxTeardownReasonLen+1)
	if _, err := comps.Mutations.TeardownResourceAuthorization(context.Background(), teardownCmd(t, long)); !errors.Is(err, mutations.ErrTeardownReasonRequired) {
		t.Fatalf("over-long reason: want ErrTeardownReasonRequired, got %v", err)
	}
	if repo.applyTrusted {
		t.Fatalf("an over-long-reason teardown must not reach the repository")
	}
}

// TestTeardownNotConfigured proves teardown fails closed with no atomic write path.
func TestTeardownNotConfigured(t *testing.T) {
	comps := mustComponents(t, Repositories{Tuples: memory.NewTuples()})
	if _, err := comps.Mutations.TeardownResourceAuthorization(context.Background(), teardownCmd(t, "why")); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("want ErrMutationsNotConfigured, got %v", err)
	}
}

// teardownSeamCommand builds a structurally valid OpTeardown Command, the shape a
// malicious/mistaken caller would push at the generic seam.
func teardownSeamCommand(t *testing.T) mutations.Command {
	t.Helper()
	return mutations.Command{

		Target:    mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
		Operation: mutations.OpTeardown,
	}
}

// TestCommandPurgeCannotWidenBound proves the end-to-end guarantee over the REAL
// memstore: a crafted actor purge supplying an oversized MaxAffectedRows cannot widen
// the blast radius. The seam overwrites it with the resolved ceiling (2), so the
// store bound bites — three rows removed > 2 → invariant_blocked, nothing removed.
func TestCommandPurgeCannotWidenBound(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
			ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	rcpt, err := svc.Mutations.PurgeResourceAuthorization(ctx, mutations.PurgeResourceAuthorizationCommand{ResourceType: "doc", ResourceID: "big"})
	if !errors.Is(err, mutations.ErrInvariantBlocked) || rcpt != nil {
		t.Fatalf("widened actor purge: receipt=%+v err=%v", rcpt, err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}
}

func TestTupleWriterApplyRejectsTeardown(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Tuples: memory.NewTuples(), Mutations: repo})

	_, err := comps.Mutations.Apply(context.Background(), teardownSeamCommand(t))
	if !errors.Is(err, mutations.ErrTeardownViaTypedMethod) {
		t.Fatalf("TupleWriter.Apply teardown: want ErrTeardownViaTypedMethod, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrTeardownViaTypedMethod must wrap sdk.ErrInvalidInput")
	}
	if repo.applyTrusted {
		t.Fatalf("rejected teardown must not reach the repository")
	}

	if _, err := comps.Mutations.TeardownResourceAuthorization(context.Background(), teardownCmd(t, "resource deleted")); err != nil {
		t.Fatalf("TeardownResourceAuthorization with a reason must still work, got %v", err)
	}
	if !repo.applyTrusted {
		t.Fatalf("typed teardown did not reach the trusted Apply path")
	}
}

func TestTupleWriterRevokeRelationship(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Tuples: memory.NewTuples(), Mutations: repo})

	cmd := mutations.RevokeRelationshipCommand{

		ResourceType: "dashboard",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      relationships.SubjectRef{Type: "user", ID: "u1"},
	}
	if _, err := comps.Mutations.RevokeRelationship(context.Background(), cmd); err != nil {
		t.Fatalf("TupleWriter.RevokeRelationship: %v", err)
	}
	if !repo.applyTrusted {
		t.Fatal("revoke did not reach repository Apply")
	}
	if repo.gotCmd.Operation != mutations.OpRevoke {
		t.Errorf("command operation = %q, want OpRevoke", repo.gotCmd.Operation)
	}
	if repo.gotCmd.Target != (mutations.Target{Kind: mutations.TargetResource, Type: cmd.ResourceType, ID: cmd.ResourceID}) || len(repo.gotCmd.Relationships) != 1 || repo.gotCmd.Relationships[0].Subject != cmd.Subject {
		t.Errorf("trusted revoke command diverges from the shared builder: %+v", repo.gotCmd)
	}

	// The trusted path still reports integrity refusal as an error.
	repo.applyErr = mutations.ErrInvariantBlocked
	rec, err := comps.Mutations.RevokeRelationship(context.Background(), cmd)
	if !errors.Is(err, mutations.ErrInvariantBlocked) || rec != nil {
		t.Fatalf("integrity refusal: receipt=%+v err=%v", rec, err)
	}
}

func TestOperationReplayAfterLowerBatchLimit(t *testing.T) {
	store := memory.New()
	repos := Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}
	original := mustComponents(t, repos, WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 2}))
	cmd := validGrantCommand(t)
	cmd.Relationships = append(cmd.Relationships, mutations.RelationshipRow{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "u2"}})
	cmd.OperationID = "committed"
	if got, err := original.Mutations.Apply(t.Context(), cmd); err != nil || got.Replayed {
		t.Fatalf("first: %+v %v", got, err)
	}
	lower := mustComponents(t, repos, WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 1}))
	if got, err := lower.Mutations.Apply(t.Context(), cmd); err != nil || !got.Replayed || got.Superseded {
		t.Fatalf("lower-limit replay: %+v %v", got, err)
	}
	for _, id := range []string{"fresh", ""} {
		cmd.OperationID = id
		if got, err := lower.Mutations.Apply(t.Context(), cmd); got != nil || !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("fresh admission: %+v %v", got, err)
		}
	}
	cmd.OperationID = "fresh"
	if got, err := original.Mutations.Apply(t.Context(), cmd); err != nil || got.Replayed {
		t.Fatalf("refused operation recorded: %+v %v", got, err)
	}
}

// -----------------------------------------------------------------------------
// Operation ledger wiring
// -----------------------------------------------------------------------------

func TestOperationsWiring(t *testing.T) {
	store := memory.New()
	if _, err := New(Repositories{Tuples: store.Tuples(), Operations: store.Operations()}); !errors.Is(err, ErrOperationsWithoutMutations) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Operations without Mutations: %v", err)
	}
	_, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: (*memory.Mutations)(nil)})
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "Repositories.Operations is typed nil") {
		t.Fatalf("typed-nil Operations: %v", err)
	}
	if _, err := mutations.NewService(store.Mutations(), mutations.WithLedger((*memory.Mutations)(nil))); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed-nil ledger option: %v", err)
	}
}

func TestOperationExpiryWiring(t *testing.T) {
	store := memory.New()
	day := 24 * time.Hour
	for _, tc := range []struct {
		name  string
		repos Repositories
		opts  []Option
		want  error
	}{
		{"expiry without Mutations", Repositories{Tuples: store.Tuples(), OperationExpiry: store.OperationExpiry()}, nil, sdk.ErrInvalidInput},
		{"typed-nil expiry", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: (*memory.Mutations)(nil)}, nil, sdk.ErrInvalidInput},
		{"retention without expiry", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()}, []Option{WithOperationRetention(30 * day)}, ErrOperationRetentionWithoutExpirer},
		{"retention without Mutations", Repositories{Tuples: store.Tuples()}, []Option{WithOperationRetention(30 * day)}, ErrOperationRetentionWithoutExpirer},
		{"retention below seven days", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry()}, []Option{WithOperationRetention(7*day - time.Nanosecond)}, ErrOperationRetentionTooShort},
		{"explicit zero retention", Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry()}, []Option{WithOperationRetention(0)}, ErrOperationRetentionTooShort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.repos, tc.opts...); !errors.Is(err, tc.want) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("New = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), OperationExpiry: store.OperationExpiry()}, WithOperationRetention(7*day)); err != nil {
		t.Fatalf("seven-day retention refused: %v", err)
	}
}

func TestExpireOperationsRequiresExpiry(t *testing.T) {
	store := memory.New()
	comps := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations()})
	if n, err := comps.Mutations.ExpireOperations(t.Context(), 10); n != 0 || !errors.Is(err, mutations.ErrOperationExpiryNotConfigured) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ExpireOperations without OperationExpiry: %d %v", n, err)
	}
	var nilService *mutations.Service
	if _, err := nilService.ExpireOperations(t.Context(), 10); !errors.Is(err, mutations.ErrOperationExpiryNotConfigured) {
		t.Fatalf("nil service ExpireOperations: %v", err)
	}
}

func TestExpireOperationsKeepsRecordsInsideRetention(t *testing.T) {
	store := memory.New()
	comps := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations(), OperationExpiry: store.OperationExpiry()})
	cmd := validGrantCommand(t)
	cmd.OperationID = "invitation:one"
	if _, err := comps.Mutations.Apply(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1} {
		if n, err := comps.Mutations.ExpireOperations(t.Context(), limit); n != 0 || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ExpireOperations(%d): %d %v", limit, n, err)
		}
	}
	if n, err := comps.Mutations.ExpireOperations(t.Context(), 10); n != 0 || err != nil {
		t.Fatalf("a record inside the default retention expired: %d %v", n, err)
	}
	if outcome, ok, err := comps.Mutations.Recorded(t.Context(), cmd.OperationID); !ok || err != nil || outcome != mutations.OutcomeApplied {
		t.Fatalf("Recorded: %q %v %v", outcome, ok, err)
	}
}

func TestFenceAndRecordedRequireLedger(t *testing.T) {
	if !errors.Is(mutations.ErrOperationLedgerNotConfigured, sdk.ErrInvalidInput) {
		t.Fatal("ErrOperationLedgerNotConfigured must wrap sdk.ErrInvalidInput")
	}
	store := memory.New()
	comps := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations()})
	if got, err := comps.Mutations.Fence(t.Context(), "invitation:one"); got != nil || !errors.Is(err, mutations.ErrOperationLedgerNotConfigured) {
		t.Fatalf("Fence without ledger: %+v %v", got, err)
	}
	if _, ok, err := comps.Mutations.Recorded(t.Context(), "invitation:one"); ok || !errors.Is(err, mutations.ErrOperationLedgerNotConfigured) {
		t.Fatalf("Recorded without ledger: %v %v", ok, err)
	}
	var nilService *mutations.Service
	if _, err := nilService.Fence(t.Context(), "invitation:one"); !errors.Is(err, mutations.ErrOperationLedgerNotConfigured) {
		t.Fatalf("nil service Fence: %v", err)
	}
}

func TestServiceFenceThenApplyReplaysFenced(t *testing.T) {
	store := memory.New()
	comps := mustComponents(t, Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations()})
	for _, id := range []string{"", "bad\x00id", strings.Repeat("x", 257)} {
		if got, err := comps.Mutations.Fence(t.Context(), id); got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Fence(%q): %+v %v", id, got, err)
		}
		if _, ok, err := comps.Mutations.Recorded(t.Context(), id); ok || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Recorded(%q): %v %v", id, ok, err)
		}
	}
	if _, ok, err := comps.Mutations.Recorded(t.Context(), "invitation:one"); ok || err != nil {
		t.Fatalf("Recorded before fence: %v %v", ok, err)
	}
	fenced, err := comps.Mutations.Fence(t.Context(), "invitation:one")
	if err != nil || fenced.Outcome != mutations.OutcomeFenced || fenced.Recorded {
		t.Fatalf("Fence: %+v %v", fenced, err)
	}
	cmd := validGrantCommand(t)
	cmd.OperationID = "invitation:one"
	got, err := comps.Mutations.Apply(t.Context(), cmd)
	if err != nil || got.Outcome != mutations.OutcomeFenced || !got.Replayed || !got.Superseded {
		t.Fatalf("Apply after fence: %+v %v", got, err)
	}
	if ok, _ := store.Relationships().CheckRelationExists(t.Context(), "doc", "d1", "viewer", "user", "u1"); ok {
		t.Fatal("fenced operation granted its tuple")
	}
	if outcome, ok, err := comps.Mutations.Recorded(t.Context(), "invitation:one"); err != nil || !ok || outcome != mutations.OutcomeFenced {
		t.Fatalf("Recorded after fence: %q %v %v", outcome, ok, err)
	}
}
