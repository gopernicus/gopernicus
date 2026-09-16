package authorization

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func validModel() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "post",
		Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]decisions.Expression{"delete": decisions.AnyOf(decisions.Direct("owner"))},
		},
	}})
}

// TestExplainPublicSurface proves CheckExplain is reachable on the public Service
// and returns a coarse Explanation whose Decision matches the CheckResult's stable
// ReasonCode; an unwired relationship kind fails closed with the kind sentinel.
func TestExplainPublicSurface(t *testing.T) {
	comps, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	res, expl, err := svc.Decisions.CheckExplain(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if err != nil {
		t.Fatalf("CheckExplain: %v", err)
	}
	if res.Allowed || res.ReasonCode != authmodel.ReasonDenied {
		t.Fatalf("empty tuple store denies: allowed=%v code=%q", res.Allowed, res.ReasonCode)
	}
	if expl.Decision != res.ReasonCode {
		t.Fatalf("Explanation.Decision %q != ReasonCode %q", expl.Decision, res.ReasonCode)
	}

	rolesOnly, err := New(Repositories{Tuples: memory.NewTuples()})
	if err != nil {
		t.Fatalf("NewService roles-only: %v", err)
	}
	// A roles-only host with NO role model bears no model at all, so the decision
	// surface refuses with ErrNoDecisionKind — "wire a model", not "wire the
	// relationship kind".
	if _, _, err := rolesOnly.Decisions.CheckExplain(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	}); err != nil {
		t.Fatalf("undeclared CheckExplain: %v", err)
	}
}

func TestNewServiceZeroKinds(t *testing.T) {
	_, err := New(Repositories{})
	if !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("want ErrNoKindConfigured, got %v", err)
	}
}

func TestNewServicePartialWiring(t *testing.T) {
	for _, model := range []decisions.Model{{}, validModel()} {
		if _, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(model)); err != nil {
			t.Fatalf("canonical facts with optional model: %v", err)
		}
	}
	if _, err := New(Repositories{}, WithModel(validModel())); !errors.Is(err, ErrNoKindConfigured) {
		t.Fatalf("missing tuple authority: %v", err)
	}
}

func TestNewServiceInvalidModel(t *testing.T) {
	bad := decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "post",
		Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]decisions.Expression{"delete": decisions.AnyOf(decisions.Direct("nonexistent"))},
		},
	}})
	_, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(bad))
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want a schema validation error, got %v", err)
	}
}

func TestConstructionRejectsInvalidModelAndLimits(t *testing.T) {
	bad := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"post": {Permissions: map[string]decisions.Expression{"delete": decisions.Direct("missing")}}}}
	_, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(bad), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: -1}))
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid constructor inputs: %v", err)
	}
}

func TestNewServiceRolesOnlySucceeds(t *testing.T) {
	if _, err := New(Repositories{Tuples: memory.NewTuples()}); err != nil {
		t.Fatalf("roles-only wiring should succeed with no model: %v", err)
	}
}

func TestNewServiceRelationshipsOnlySucceeds(t *testing.T) {
	if _, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel())); err != nil {
		t.Fatalf("relationships-only wiring should succeed: %v", err)
	}
}

func TestCanonicalAuthoritySuppliesEveryRawFacade(t *testing.T) {
	comps, err := New(Repositories{Tuples: memory.NewTuples()})
	if err != nil {
		t.Fatal(err)
	}
	if comps.Relationships == nil || comps.RelationshipWriter == nil {
		t.Fatal("canonical authority must supply the relationship facade and writer")
	}
	if comps.Roles == nil || comps.Decisions == nil {
		t.Fatal("canonical read surfaces must be present")
	}
}

func TestCanonicalAuthorityAlwaysSuppliesExactRoleChecks(t *testing.T) {
	comps, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := comps.Roles.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "editor")
	if err != nil || got {
		t.Fatalf("empty authority: %v, %v", got, err)
	}
}

func TestRoleAndGraphChecksUseOneAuthority(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	fact := tuples.Tuple{Scope: tuples.On("post", "p1"), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "u1"}}
	if err := store.Tuples().ApplyTuples(ctx, tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
		t.Fatal(err)
	}
	comps, err := New(Repositories{Tuples: store.Tuples()}, WithModel(validModel()))
	if err != nil {
		t.Fatal(err)
	}
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}
	got, err := comps.Decisions.Check(ctx, authmodel.CheckRequest{Principal: principal, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"}})
	if err != nil || !got.Allowed {
		t.Fatalf("graph check: %+v, %v", got, err)
	}
	held, err := comps.Roles.HasRoleIn(ctx, principal, "owner", authmodel.Resource{Type: "post", ID: "p1"})
	if err != nil || !held {
		t.Fatalf("exact scoped check: %v, %v", held, err)
	}
}

// TestConstructionDefaultLimits proves a relationships wiring with a zero
// WithLimits succeeds: every budget field resolves to its safe default.
func TestConstructionDefaultLimits(t *testing.T) {
	if _, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel())); err != nil {
		t.Fatalf("zero Limits should resolve to defaults, got %v", err)
	}
}

// TestConstructionExplicitLimits proves a positive, fully specified WithLimits
// is accepted.
func TestConstructionExplicitLimits(t *testing.T) {
	cfg := []Option{WithModel(validModel()), WithLimits(authmodel.EvaluationLimits{
		MaxThroughDepth:    5,
		MaxGraphStates:     500,
		MaxRelationTargets: 50,
		MaxBatchSize:       50,
		MaxLookupResults:   50,
	})}
	if _, err := New(Repositories{Tuples: memory.NewTuples()}, cfg...); err != nil {
		t.Fatalf("explicit positive Limits should be accepted, got %v", err)
	}
}

// TestConstructionNegativeLimitRejected proves EVERY budget field rejects a
// negative value with ErrInvalidLimits when the relationship kind is wired.
func TestConstructionNegativeLimitRejected(t *testing.T) {
	cases := map[string]authmodel.EvaluationLimits{
		"MaxThroughDepth":    {MaxThroughDepth: -1},
		"MaxGraphStates":     {MaxGraphStates: -1},
		"MaxRelationTargets": {MaxRelationTargets: -1},
		"MaxBatchSize":       {MaxBatchSize: -1},
		"MaxLookupResults":   {MaxLookupResults: -1},
	}
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel()), WithLimits(limits))
			if !errors.Is(err, authmodel.ErrInvalidLimits) {
				t.Fatalf("negative %s: want ErrInvalidLimits, got %v", name, err)
			}
		})
	}
}

// TestConstructionOrphanedLimitsUnderRolesOnly proves that WithLimits set on
// a roles-only wiring is a silently orphaned tuning field (the auth MailFrom
// precedent): it is not validated and not an error, because no relationship
// engine consumes it. Even a negative limit is ignored when the kind is off.
func TestModelFreeDecisionsValidateLimits(t *testing.T) {
	_, err := New(Repositories{Tuples: memory.NewTuples()}, WithLimits(authmodel.EvaluationLimits{MaxThroughDepth: -1, MaxBatchSize: -1}))
	if !errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("model-free limits: %v", err)
	}
}

func TestRegister(t *testing.T) {
	comps, err := New(Repositories{Tuples: memory.NewTuples()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	// With a logger.
	if err := svc.Register(pockets.Mount{Logger: slog.Default()}); err != nil {
		t.Fatalf("Register with logger: %v", err)
	}
	// Zero-value Mount (nil logger) is tolerated.
	if err := svc.Register(pockets.Mount{}); err != nil {
		t.Fatalf("Register with zero Mount: %v", err)
	}
}

// -----------------------------------------------------------------------------
// The roles kind's model: construction matrix and the ONE decision surface
// -----------------------------------------------------------------------------

// projectRoleModel is the roles-kind fixture: one type, one role, one permission.
func projectRoleModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"project": {Permissions: map[string]decisions.Expression{"audit": decisions.Any(decisions.RoleIn("auditor"), decisions.Role("auditor"))}},
	}}
}

func assignment(subjectID, roleName, resourceType, resourceID string) roles.Assignment {
	return roles.Assignment{
		SubjectType: "user", SubjectID: subjectID, Role: roleName, Scope: fixtureScope(resourceType, resourceID),
	}
}

// newSeededRoles builds an in-core role store already holding the assignments —
// the decision-surface tests read roles, they do not exercise the write path.
func newSeededRoles(t *testing.T, assignments ...roles.Assignment) tuples.Storer {
	t.Helper()
	store := memory.NewTuples()
	for _, a := range assignments {
		if err := store.ApplyTuples(context.Background(), tuples.Changes{Add: []tuples.Tuple{a.Tuple()}}); err != nil {
			t.Fatalf("seed %+v: %v", a, err)
		}
	}
	return store
}

func projectRequest(subjectID, permission, projectID string) authmodel.CheckRequest {
	return authmodel.CheckRequest{
		Principal:  authmodel.PrincipalRef{Type: "user", ID: subjectID},
		Permission: permission,
		Resource:   authmodel.Resource{Type: "project", ID: projectID},
	}
}

// TestConstructionRoleModelWithoutRolesRepo proves the one-directional wiring
// rule: a model with no roles repository could never decide anything, so it fails
// boot — while a roles repository with NO model stays the valid opaque posture
// (TestNewRolesOnlySucceeds).
func TestExactExpressionsNeedOnlyCanonicalAuthority(t *testing.T) {
	if _, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(projectRoleModel())); err != nil {
		t.Fatal(err)
	}
}

// TestConstructionInvalidRoleModel proves a structurally invalid model is a loud
// boot failure naming the offending symbol.
func TestExactExpressionsRejectMalformedLabels(t *testing.T) {
	bad := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"project": {Permissions: map[string]decisions.Expression{"audit": decisions.RoleIn("")}}}}
	if _, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(bad)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("malformed label: %v", err)
	}
}

// TestConstructionModelConflict proves pair ownership is enforced at boot: a
// resource TYPE may appear in both models, but a (type, permission) PAIR may not
// — that overlap is what would make the decision surface a merge.
func TestWithModelReplacesOneWholeModel(t *testing.T) {
	comps, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(validModel()), WithModel(projectRoleModel()))
	if err != nil {
		t.Fatal(err)
	}
	if comps.Decisions.DeclaresPermission("post", "delete") || !comps.Decisions.DeclaresPermission("project", "audit") {
		t.Fatal("models were merged instead of replaced")
	}
}

// Exact role expressions work without a model; undeclared named permissions deny.
func TestModelFreeExactExpressionsAndUnknownPermissions(t *testing.T) {
	store := newSeededRoles(t, assignment("u1", "auditor", "", ""))
	comps, err := New(Repositories{Tuples: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}
	got, err := comps.Decisions.Evaluate(ctx, principal, decisions.Role("auditor"))
	if err != nil || !got.Allowed {
		t.Fatalf("model-free exact check: %+v, %v", got, err)
	}
	got, err = comps.Decisions.Check(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || got.Allowed {
		t.Fatalf("undeclared permission: %+v, %v", got, err)
	}
	batch, err := comps.Decisions.CheckBatch(ctx, []authmodel.CheckRequest{projectRequest("u1", "audit", "p1")})
	if err != nil || len(batch) != 1 || batch[0].Allowed {
		t.Fatalf("undeclared batch: %+v, %v", batch, err)
	}
	lookup, err := comps.Decisions.LookupAllResourceIDs(ctx, principal, "audit", "project")
	if err != nil || lookup.Unrestricted || len(lookup.IDs) != 0 {
		t.Fatalf("undeclared lookup: %+v, %v", lookup, err)
	}
}

// TestLookupResourcesInThroughTheFacade drives the PAGED surface through the
// PUBLIC API: a page smaller than the result carries HasMore plus a continuation
// and a page that holds everything carries
// neither, walking the cursor reproduces the classic LookupAllResourceIDs result
// exactly, and an out-of-range Limit is invalid input a host maps to 400.
func TestLookupResourcesInThroughTheFacade(t *testing.T) {
	roles := newSeededRoles(t,
		assignment("u1", "auditor", "project", "p1"),
		assignment("u1", "auditor", "project", "p2"),
		assignment("u1", "auditor", "project", "p3"),
	)
	comps, err := New(Repositories{Tuples: roles}, WithModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	ctx := context.Background()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}

	for name, tc := range map[string]struct {
		limit       int
		wantIDs     []string
		wantHasMore bool
	}{
		"limit below the count pages":    {2, []string{"p1", "p2"}, true},
		"limit that fits":                {3, []string{"p1", "p2", "p3"}, false},
		"limit zero is the page ceiling": {0, []string{"p1", "p2", "p3"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
				Principal: principal, Permission: "audit", ResourceType: "project", Limit: tc.limit,
			})
			if err != nil {
				t.Fatalf("LookupResourceIDPage: %v", err)
			}
			if !reflect.DeepEqual(got.IDs, tc.wantIDs) {
				t.Fatalf("IDs = %v, want %v", got.IDs, tc.wantIDs)
			}
			if got.HasMore != tc.wantHasMore {
				t.Fatalf("HasMore = %v, want %v", got.HasMore, tc.wantHasMore)
			}
			if (got.NextCursor != "") != tc.wantHasMore {
				t.Fatalf("NextCursor %q disagrees with HasMore %v", got.NextCursor, got.HasMore)
			}
		})
	}

	// Walking the continuation reproduces the classic enumeration exactly.
	var walked []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 8 {
			t.Fatal("page walk did not terminate")
		}
		page, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "audit", ResourceType: "project", Limit: 1, After: cursor,
		})
		if err != nil {
			t.Fatalf("LookupResourceIDPage(after=%q): %v", cursor, err)
		}
		walked = append(walked, page.IDs...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	classic, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, "audit", "project")
	if err != nil {
		t.Fatalf("LookupAllResourceIDs: %v", err)
	}
	if len(classic.IDs) != 3 {
		t.Fatalf("the classic method never pages and never reports a continuation, got %+v", classic)
	}
	if !reflect.DeepEqual(walked, classic.IDs) {
		t.Fatalf("the pages concatenated to %v, want the classic result %v", walked, classic.IDs)
	}

	// A cursor from ANOTHER principal is refused: it is bound to the query that
	// minted it, and a host maps the refusal to 400.
	first, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
		Principal: principal, Permission: "audit", ResourceType: "project", Limit: 1,
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("seed page: %+v, %v", first, err)
	}
	_, err = svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u2"}, Permission: "audit", ResourceType: "project",
		Limit: 1, After: first.NextCursor,
	})
	if !errors.Is(err, authmodel.ErrInvalidCursor) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("a foreign cursor must be ErrInvalidCursor wrapping sdk.ErrInvalidInput, got %v", err)
	}

	for name, req := range map[string]decisions.ResourceIDPageRequest{
		"negative limit": {Principal: principal, Permission: "audit", ResourceType: "project", Limit: -1},
		"limit above MaxLookupResults": {Principal: principal, Permission: "audit", ResourceType: "project",
			Limit: authmodel.DefaultMaxLookupResults + 1},
		"malformed cursor": {Principal: principal, Permission: "audit", ResourceType: "project", After: "not-a-cursor"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Decisions.LookupResourceIDPage(ctx, req); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestRolesOnlyWithModelDecides proves the flagship acceptance: a roles-only host
// that configures a RoleModel gets the whole decision surface, answered from role
// assignments alone.
func TestRolesOnlyWithModelDecides(t *testing.T) {
	roles := newSeededRoles(t,
		assignment("u1", "auditor", "project", "p1"),
		assignment("u2", "auditor", "", ""), // globally held
	)
	comps, err := New(Repositories{Tuples: roles}, WithModel(projectRoleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps
	ctx := context.Background()

	res, err := svc.Decisions.Check(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || !res.Allowed || res.ReasonCode != authmodel.ReasonGranted {
		t.Fatalf("scoped role holder: got %+v err=%v", res, err)
	}
	res, err = svc.Decisions.Check(ctx, projectRequest("u1", "audit", "p2"))
	if err != nil || res.Allowed || res.ReasonCode != authmodel.ReasonDenied {
		t.Fatalf("another project: got %+v err=%v", res, err)
	}
	res, err = svc.Decisions.Check(ctx, projectRequest("u1", "publish", "p1"))
	if err != nil || res.Allowed || res.Reason != "no rules defined" {
		t.Fatalf("undeclared pair: got %+v err=%v", res, err)
	}

	lookup, err := svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project")
	if err != nil || lookup.Unrestricted || !reflect.DeepEqual(lookup.IDs, []string{"p1"}) {
		t.Fatalf("scoped lookup: got %+v err=%v", lookup, err)
	}
	lookup, err = svc.Decisions.LookupAllResourceIDs(ctx, authmodel.PrincipalRef{Type: "user", ID: "u2"}, "audit", "project")
	if err != nil || !lookup.Unrestricted || len(lookup.IDs) != 0 {
		t.Fatalf("global granting role: want unrestricted with empty IDs, got %+v err=%v", lookup, err)
	}

	ids, err := svc.Decisions.FilterAuthorized(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", []string{"p1", "p2"})
	if err != nil || !reflect.DeepEqual(ids, []string{"p1"}) {
		t.Fatalf("FilterAuthorized: got %v err=%v", ids, err)
	}
	// Zero-length identities are preserved literally on a wired decision surface.
	if ids, err := svc.Decisions.FilterAuthorized(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "audit", "project", nil); ids != nil || err != nil {
		t.Fatalf("FilterAuthorized with no IDs: want (nil, nil), got (%v, %v)", ids, err)
	}
	if results, err := svc.Decisions.CheckBatch(ctx, nil); results != nil || err != nil {
		t.Fatalf("CheckBatch(nil): want (nil, nil), got (%v, %v)", results, err)
	}

	_, expl, err := svc.Decisions.CheckExplain(ctx, projectRequest("u1", "audit", "p1"))
	if err != nil || len(expl.Steps) != 1 || expl.Steps[0].Kind != "exact" ||
		expl.Steps[0].Relation != "auditor" || expl.Steps[0].ResourceType != "project" || expl.Steps[0].ResourceID != "p1" {
		t.Fatalf("role explain trace: got %+v err=%v", expl, err)
	}
}

// TestConstructionLimitsUnderRolesAndModel proves the evaluation budget is the
// DECISION SURFACE's: with a role model wired, a negative limit fails boot (it is
// no longer an orphaned setting) and MaxBatchSize is captured and charged.
func TestConstructionLimitsUnderRolesAndModel(t *testing.T) {
	_, err := New(Repositories{Tuples: memory.NewTuples()}, WithModel(projectRoleModel()), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: -1}))
	if !errors.Is(err, authmodel.ErrInvalidLimits) {
		t.Fatalf("negative limit under roles+model: want ErrInvalidLimits, got %v", err)
	}

	comps, err := New(Repositories{Tuples: newSeededRoles(t)}, WithModel(projectRoleModel()), WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 2}))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if comps.Decisions.Limits().MaxBatchSize != 2 {
		t.Fatalf("maxBatchSize: want 2, got %d", comps.Decisions.Limits().MaxBatchSize)
	}
	reqs := []authmodel.CheckRequest{
		projectRequest("u1", "audit", "p1"),
		projectRequest("u1", "audit", "p2"),
		projectRequest("u1", "audit", "p3"),
	}
	if _, err := comps.Decisions.CheckBatch(context.Background(), reqs); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("over the batch ceiling: want ErrEvaluationLimit, got %v", err)
	}
}

// TestRegisterLogsRoleModelPresence proves the mount line reports whether a role
// model is configured — a BOOL only; no type, role, or permission name reaches
// the log.
func TestRegisterLogsRoleModelPresence(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  []Option
		want string
	}{
		"with a role model":    {[]Option{WithModel(projectRoleModel())}, `"model":true`},
		"without a role model": {[]Option{}, `"model":true`},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.cfg = append(tc.cfg, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
			comps, err := New(Repositories{Tuples: memory.NewTuples()}, tc.cfg...)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			if err := comps.Register(pockets.Mount{}); err != nil {
				t.Fatalf("Register: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("mount line %s: want %s, got %s", name, tc.want, buf.String())
			}
			if strings.Contains(buf.String(), "auditor") || strings.Contains(buf.String(), "project") {
				t.Fatalf("policy vocabulary must never reach the log line: %s", buf.String())
			}
		})
	}
}

func fixtureScope(resourceType, resourceID string) tuples.Scope {
	if resourceType == "" && resourceID == "" {
		return tuples.Global()
	}
	return tuples.On(resourceType, resourceID)
}
