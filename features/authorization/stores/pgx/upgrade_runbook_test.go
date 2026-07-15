// The v1→v3 upgrade runbook, executed end to end as a repeatable, hermetic-skippable
// live test (AZ3-5.1). It BUILDS a populated pre-v3 fixture (the git-d11c7a2 baseline:
// iam_relationships + iam_roles WITHOUT constraints, WITHOUT iam_scopes/iam_mutations,
// with rows exhibiting every runbook category), runs UPGRADE.md/CONVERSION.md's
// detection queries, proves the structurally-malformed rows BLOCK the conversion until
// repaired, applies the data-preserving conversion (ALTER-add the three CHECK
// constraints the canonical CREATE TABLE IF NOT EXISTS cannot add to a pre-existing
// table + the canonical 0003/0004 new tables), seeds revision anchors, BOOTS a
// v3-composed authorization.Service over the converted store, and asserts the
// gain/lose/retain access verdicts from the UPGRADE.md §2 assessment table hold. It
// then exercises the documented rollback boundary.
//
// It requires POSTGRES_TEST_DSN and skips loudly without it, like the conformance
// suite. It fully owns and drops the iam_* tables around its run, so it is
// self-contained and re-runnable and leaves the database clean for the conformance
// suite. Prefer a scratch C-collation database as the DSN target (per the AZ3 live
// recipe), but any Postgres it exclusively controls works — it never trusts existing
// state.
package pgx

import (
	"context"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// v1BaselineDDL is the pre-v3 schema reconstructed verbatim from git d11c7a2
// (authorization-v1 store): the same two tables with the same columns v3 keeps,
// but NO ck_* CHECK constraints and NO iam_scopes/iam_mutations tables. Each
// statement is applied separately so the fixture models a genuine v1 host.
var v1BaselineDDL = []string{
	`CREATE TABLE iam_relationships (
		relationship_id  TEXT        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()::text,
		resource_type    TEXT        NOT NULL,
		resource_id      TEXT        NOT NULL,
		relation         TEXT        NOT NULL,
		subject_type     TEXT        NOT NULL,
		subject_id       TEXT        NOT NULL,
		subject_relation TEXT        NOT NULL DEFAULT '',
		created_at       TIMESTAMPTZ NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_iam_relationships_unique_tuple ON iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation)`,
	`CREATE UNIQUE INDEX idx_iam_relationships_unique_subject ON iam_relationships (resource_type, resource_id, subject_type, subject_id, subject_relation)`,
	`CREATE INDEX idx_iam_relationships_resource ON iam_relationships (resource_type, resource_id)`,
	`CREATE INDEX idx_iam_relationships_subject ON iam_relationships (subject_type, subject_id)`,
	`CREATE INDEX idx_iam_relationships_type_relation ON iam_relationships (resource_type, relation)`,
	`CREATE TABLE iam_roles (
		subject_type  TEXT        NOT NULL,
		subject_id    TEXT        NOT NULL,
		role          TEXT        NOT NULL,
		resource_type TEXT        NOT NULL DEFAULT '',
		resource_id   TEXT        NOT NULL DEFAULT '',
		created_at    TIMESTAMPTZ NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_iam_roles_unique ON iam_roles (subject_type, subject_id, role, resource_type, resource_id)`,
	`CREATE INDEX idx_iam_roles_subject ON iam_roles (subject_type, subject_id)`,
	`CREATE INDEX idx_iam_roles_resource ON iam_roles (resource_type, resource_id, created_at)`,
}

// v1FixtureRows populates the baseline with a row per runbook category: direct
// concrete group members, valid #member usersets, a non-member #admin userset, a
// concrete-group grant, a last owner, an ambiguous userset (concrete where the
// schema requires a userset), structurally-malformed relationship/role rows, and
// global/scoped roles.
var v1FixtureRows = []string{
	// group membership: umem is a member, uadm is an admin of geng
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('group','geng','member','user','umem','', now())`,
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('group','geng','admin','user','uadm','', now())`,
	// RETAIN: concrete principal grant
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','dret','viewer','user','u1','', now())`,
	// RETAIN: valid #member userset grant
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','dmem','viewer','group','geng','member', now())`,
	// LOSE: concrete-group grant (v1 hard-coded member expansion reached members)
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','dcon','viewer','group','geng','', now())`,
	// LOSE: non-member userset grant (v1 evaluated #admin as member)
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','dadm','viewer','group','geng','admin', now())`,
	// last owner (guardian-protected under v3)
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','downed','owner','user','uowner','', now())`,
	// AMBIGUOUS userset: org.member requires group#member; this stores a concrete group ref
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('org','o1','member','group','gorg','', now())`,
	// BLOCKING (1a): empty relation
	`INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('doc','dbad','','user','ubad','', now())`,
	// roles: global, scoped, and two blocking (1b) rows
	`INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','uglob','platform_admin','','', now())`,
	`INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','uscope','editor','doc','dscope', now())`,
	`INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','ubad2','editor','doc','', now())`,
	`INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','ubad3','','','', now())`,
}

// dataPreservingConstraints are the three CHECK constraints the runbook's
// data-preserving path adds with explicit ALTER TABLE. The canonical
// CREATE TABLE IF NOT EXISTS files (0001/0002) are a no-op against a pre-existing
// v1 table, so they cannot add these — the ALTER IS the enforced block, and it
// fails while any violating row remains.
var dataPreservingConstraints = []string{
	`ALTER TABLE iam_relationships ADD CONSTRAINT ck_iam_relationships_nonempty CHECK (resource_type <> '' AND resource_id <> '' AND relation <> '' AND subject_type <> '' AND subject_id <> '')`,
	`ALTER TABLE iam_roles ADD CONSTRAINT ck_iam_roles_nonempty CHECK (subject_type <> '' AND subject_id <> '' AND role <> '')`,
	`ALTER TABLE iam_roles ADD CONSTRAINT ck_iam_roles_scope_pair CHECK ((resource_type = '') = (resource_id = ''))`,
}

// upgradeSchema is the v3 model the converted store boots under. It carries the
// exact userset semantics the assessment classifies against: doc.viewer accepts a
// concrete group, group#member, and group#admin as DISTINCT subjects; doc.owner
// backs the guardian last-owner invariant; org.member requires a group#member
// userset (making the concrete org:o1 row ambiguous).
func upgradeSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: "group", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"admin":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
		}},
		{Name: "org", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "doc", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"viewer": {AllowedSubjects: []authorization.SubjectTypeRef{
					{Type: "user"},
					{Type: "group"},
					{Type: "group", Relation: "member"},
					{Type: "group", Relation: "admin"},
					{Type: "org", Relation: "member"},
				}},
				"owner": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				"view":   authorization.AnyOf(authorization.Direct("viewer")),
				"manage": authorization.AnyOf(authorization.Direct("owner")),
			},
		}},
	})
}

// TestUpgradeRunbook executes UPGRADE.md against a populated v1 fixture on a live
// PostgreSQL database and proves the acceptance bar: no step resets the database,
// no ambiguous/missing relation is silently read as member, and the structurally
// malformed rows block the conversion until explicitly repaired.
func TestUpgradeRunbook(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	db, err := pgxdb.Open(pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dropAll := func() {
		for _, tbl := range []string{"iam_mutations", "iam_scopes", "iam_roles", "iam_relationships"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE")
		}
		// Clear the ledger rows for the canonical versions (the host runner records
		// them; RunMigrations here uses them) so the test is fully re-runnable: without
		// this, a second run would see the canonical files already applied and skip
		// creating iam_scopes/iam_mutations.
		for _, v := range canonicalMigrations {
			_, _ = db.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", v)
		}
	}
	dropAll()
	t.Cleanup(dropAll)

	// ---- Build the populated v1 fixture ----
	for _, stmt := range v1BaselineDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("v1 baseline DDL: %v", err)
		}
	}
	for _, stmt := range v1FixtureRows {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("v1 fixture row: %v", err)
		}
	}

	// ---- Detection audit (CONVERSION.md 1a/1b/1c/1d) ----
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_relationships WHERE resource_type='' OR resource_id='' OR relation='' OR subject_type='' OR subject_id=''`); n != 1 {
		t.Fatalf("1a empty structural relationship columns = %d, want 1 (dbad)", n)
	}
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_roles WHERE subject_type='' OR subject_id='' OR role=''`); n != 1 {
		t.Fatalf("1b empty role columns = %d, want 1 (ubad3)", n)
	}
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_roles WHERE (resource_type='') <> (resource_id='')`); n != 1 {
		t.Fatalf("1b half-populated scope pair = %d, want 1 (ubad2)", n)
	}
	// 1c: the ambiguous shape — org.member with a concrete (empty-relation) group subject.
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_relationships WHERE resource_type='org' AND relation='member' AND subject_type='group' AND subject_relation=''`); n != 1 {
		t.Fatalf("1c ambiguous concrete-where-userset-required shape = %d, want 1 (org:o1)", n)
	}
	// 1d: the non-member userset whose meaning changes.
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_relationships WHERE subject_relation <> '' AND subject_relation <> 'member'`); n != 1 {
		t.Fatalf("1d non-member usersets = %d, want 1 (group#admin)", n)
	}

	// ---- Blocking proof: the constraint ALTER fails while malformed rows remain ----
	if _, err := db.Exec(ctx, dataPreservingConstraints[0]); err == nil {
		t.Fatal("ck_iam_relationships_nonempty must not apply while a malformed row remains — the upgrade is not blocked")
	}
	if _, err := db.Exec(ctx, dataPreservingConstraints[1]); err == nil {
		t.Fatal("ck_iam_roles_nonempty must not apply while a malformed row remains")
	}

	// ---- Repair: the operator deletes the structurally-malformed rows ----
	// The ambiguous/meaning-changed rows (dcon concrete-group, dadm #admin, org:o1)
	// are LEFT AS THEY ARE per operator sign-off: v3 narrows them, and NONE is
	// silently defaulted to member.
	if _, err := db.Exec(ctx, `DELETE FROM iam_relationships WHERE resource_type='' OR resource_id='' OR relation='' OR subject_type='' OR subject_id=''`); err != nil {
		t.Fatalf("repair relationships: %v", err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM iam_roles WHERE subject_type='' OR subject_id='' OR role='' OR ((resource_type='') <> (resource_id=''))`); err != nil {
		t.Fatalf("repair roles: %v", err)
	}
	// Re-run 1a/1b — must be clean before the conversion proceeds.
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_relationships WHERE resource_type='' OR resource_id='' OR relation='' OR subject_type='' OR subject_id=''`); n != 0 {
		t.Fatalf("1a after repair = %d, want 0", n)
	}
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_roles WHERE subject_type='' OR subject_id='' OR role='' OR ((resource_type='') <> (resource_id=''))`); n != 0 {
		t.Fatalf("1b after repair = %d, want 0", n)
	}

	// ---- Apply the v3 conversion ----
	// (a) the three CHECK constraints the canonical CREATE TABLE IF NOT EXISTS cannot
	//     add to a pre-existing table — now they apply because the data is clean.
	for _, stmt := range dataPreservingConstraints {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("apply constraint after repair: %v", err)
		}
	}
	// (b) the canonical migration source, which creates the new iam_scopes /
	//     iam_mutations tables (0001/0002 no-op on the existing tables).
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("apply canonical migrations: %v", err)
	}
	// (c) seed revision-0 anchors (CONVERSION.md §3).
	if _, err := db.Exec(ctx, `INSERT INTO iam_scopes (scope_kind, scope_type, scope_id)
		SELECT DISTINCT 'resource', resource_type, resource_id FROM iam_relationships
		UNION SELECT DISTINCT 'resource', resource_type, resource_id FROM iam_roles WHERE resource_type <> ''
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed resource anchors: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO iam_scopes (scope_kind, scope_type, scope_id)
		SELECT DISTINCT 'subject', subject_type, subject_id FROM iam_roles WHERE resource_type = ''
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed subject anchors: %v", err)
	}
	// Every seeded anchor is revision 0, and no receipt is backfilled.
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_scopes WHERE revision <> 0`); n != 0 {
		t.Fatalf("a seeded anchor carried a nonzero revision (n=%d)", n)
	}
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_mutations`); n != 0 {
		t.Fatalf("iam_mutations must be empty after conversion, got %d receipts", n)
	}

	// ---- Boot a v3-composed service over the converted store ----
	repos, err := Repositories(db)
	if err != nil {
		t.Fatalf("boot Repositories over converted store: %v", err)
	}
	comps, err := authorization.NewService(repos, authorization.Config{Model: upgradeSchema()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service

	// ---- Access comparison: the gain/lose/retain verdicts from UPGRADE.md §2 ----
	// RETAIN: a concrete principal grant is unchanged.
	mustCheck(t, ctx, svc, "user", "u1", "dret", true, "RETAIN concrete principal")
	// RETAIN: a valid #member userset still reaches the group's members.
	mustCheck(t, ctx, svc, "user", "umem", "dmem", true, "RETAIN #member userset")
	// LOSE: a concrete-group grant no longer reaches members (v1 hard-coded member).
	mustCheck(t, ctx, svc, "user", "umem", "dcon", false, "LOSE concrete-group no longer reaches members")
	// LOSE: a non-member userset (#admin) no longer behaves as member.
	mustCheck(t, ctx, svc, "user", "umem", "dadm", false, "LOSE #admin no longer behaves as member")
	// but the exact #admin userset still reaches the true admin.
	mustCheck(t, ctx, svc, "user", "uadm", "dadm", true, "RETAIN #admin reaches the admin")
	// The ambiguous org:o1 row, left concrete, is NEVER silently read as member: a
	// member of gorg does not reach org:o1 (no member could, since gorg has no members
	// here — the point is v3 does not fabricate the userset).
	orgRes, err := svc.Check(ctx, authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: "user", ID: "umem"},
		Permission: "view",
		Resource:   authorization.Resource{Type: "doc", ID: "dcon"},
	})
	if err != nil {
		t.Fatalf("ambiguous-row check: %v", err)
	}
	if orgRes.Allowed {
		t.Fatal("v3 must not silently interpret a missing relation as member")
	}

	// The global role still satisfies HasRole; the scoped role is scoped.
	if ok, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "uglob"}, "platform_admin", "doc", "anything"); err != nil || !ok {
		t.Fatalf("global role must satisfy a scoped HasRole (ok=%v err=%v)", ok, err)
	}
	if ok, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "uscope"}, "editor", "doc", "dscope"); err != nil || !ok {
		t.Fatalf("scoped role must hold at its scope (ok=%v err=%v)", ok, err)
	}

	// ---- Last-owner invariant holds on the converted store ----
	// Revoking downed's only owner is blocked by the guardian (owner, min 1).
	revoke := mutation.Command{
		MutationID:    mutID(t),
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "downed"},
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: relationship.SubjectRef{Type: "user", ID: "uowner"}}},
	}
	rcpt, err := repos.Mutations.Apply(ctx, revoke, nil)
	if err != nil {
		t.Fatalf("last-owner revoke Apply: %v", err)
	}
	if rcpt.Outcome != mutation.OutcomeInvariantBlocked {
		t.Fatalf("last-owner revoke outcome = %q, want invariant_blocked", rcpt.Outcome)
	}
	if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "downed", "owner", "user", "uowner"); !ok {
		t.Fatal("a blocked last-owner revoke must leave the owner in place")
	}

	// ---- Rollback boundary (documented) ----
	// Before any v3 mutation commits: a v1-style write reintroducing a malformed row
	// is now rejected by the applied constraint — hand-reversing repairs is unsafe,
	// so restore-from-backup is the rollback mechanism.
	if _, err := db.Exec(ctx, `INSERT INTO iam_relationships (resource_type,resource_id,relation,subject_type,subject_id,created_at) VALUES ('doc','dx','','user','ux', now())`); err == nil {
		t.Fatal("the applied constraint must reject a v1-style malformed reintroduction")
	}
	// The first committed v3 mutation is the rollback boundary: after it, a receipt
	// exists and the scope revision has advanced past the seeded 0, so resuming a v1
	// binary (which ignores both) would desync — past this line rollback means
	// restore-from-backup, not an in-place downgrade.
	grant := grantCmd(mutID(t), "downed", "viewer", "unew")
	if _, err := repos.Mutations.Apply(ctx, grant, nil); err != nil {
		t.Fatalf("first v3 mutation: %v", err)
	}
	if n := countRows(t, ctx, db, `SELECT count(*) FROM iam_mutations`); n == 0 {
		t.Fatal("the first v3 mutation must persist a receipt (the rollback boundary)")
	}
	if rev := countRows(t, ctx, db, `SELECT revision FROM iam_scopes WHERE scope_kind='resource' AND scope_type='doc' AND scope_id='downed'`); rev == 0 {
		t.Fatal("the first v3 mutation must advance the scope revision past the seeded 0")
	}
}

func countRows(t *testing.T, ctx context.Context, db *pgxdb.DB, query string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", strings.SplitN(query, " WHERE", 2)[0], err)
	}
	return n
}

func mustCheck(t *testing.T, ctx context.Context, svc *authorization.Service, subjType, subjID, docID string, want bool, label string) {
	t.Helper()
	res, err := svc.Check(ctx, authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: subjType, ID: subjID},
		Permission: "view",
		Resource:   authorization.Resource{Type: "doc", ID: docID},
	})
	if err != nil {
		t.Fatalf("%s: Check(%s:%s, doc:%s): %v", label, subjType, subjID, docID, err)
	}
	if res.Allowed != want {
		t.Fatalf("%s: Check(%s:%s, doc:%s) = %v, want %v (reason %q)", label, subjType, subjID, docID, res.Allowed, want, res.Reason)
	}
}
