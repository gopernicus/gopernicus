//go:build integration

// Live schema-probe tests hit the same Turso/libSQL database as the conformance
// suite (TURSO_DATABASE_URL/TURSO_AUTH_TOKEN). They prove the AZ3-2.1 greenfield
// schema APPLIES cleanly and that iam_scopes and iam_mutations — which have no Go
// consumer yet — exist with their CHECK constraints ENFORCED live, not merely
// declared in text. The libSQL container is shared/persistent, so the probe DELETEs
// its own rows before and after (matching the conformance suite's DELETE isolation)
// so stale rows from an earlier run cannot poison results.
package turso

import (
	"context"
	"testing"
)

// TestSchemaProbe applies the canonical migrations and asserts the new revision /
// receipt tables exist and that every AZ3-2.1 constraint rejects a violating row
// while a well-formed row is accepted.
func TestSchemaProbe(t *testing.T) {
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	ctx := context.Background()

	for _, tbl := range []string{"iam_scopes", "iam_mutations"} {
		if err := probeTable(ctx, db, tbl); err != nil {
			t.Fatalf("probe %s: %v", tbl, err)
		}
	}

	clean := func() {
		_, _ = db.Exec(ctx, "DELETE FROM iam_scopes")
		_, _ = db.Exec(ctx, "DELETE FROM iam_mutations")
	}
	clean()
	t.Cleanup(clean)

	// Well-formed rows are accepted.
	if _, err := db.Exec(ctx,
		`INSERT INTO iam_scopes (scope_kind, scope_type, scope_id, revision) VALUES ('resource', 'doc', 'd1', 0)`); err != nil {
		t.Fatalf("valid iam_scopes insert rejected: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at)
		 VALUES ('m0000000000000000000000001', 'resource', 'doc', 'd1', 'grant', 'gopernicus.authorization.mutation/1', 'deadbeef', 'applied', 1, 'cafef00d', '2026-07-14T00:00:00Z')`); err != nil {
		t.Fatalf("valid iam_mutations insert rejected: %v", err)
	}
	// A NULL expires_at (permanent retention, the default posture) is accepted.
	if _, err := db.Exec(ctx,
		`INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at, expires_at)
		 VALUES ('m0000000000000000000000002', 'subject', 'user', 'u1', 'role_assign', 'gopernicus.authorization.mutation/1', 'beefdead', 'no_change', 0, 'cafef00d', '2026-07-14T00:00:00Z', NULL)`); err != nil {
		t.Fatalf("permanent-retention iam_mutations insert rejected: %v", err)
	}

	// Every constraint rejects a violating row.
	rejects := []struct {
		name string
		sql  string
	}{
		{"scope invalid kind", `INSERT INTO iam_scopes (scope_kind, scope_type, scope_id, revision) VALUES ('bogus', 'doc', 'd2', 0)`},
		{"scope negative revision", `INSERT INTO iam_scopes (scope_kind, scope_type, scope_id, revision) VALUES ('resource', 'doc', 'd3', -1)`},
		{"scope empty type", `INSERT INTO iam_scopes (scope_kind, scope_type, scope_id, revision) VALUES ('resource', '', 'd4', 0)`},
		{"mutation invalid kind", `INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at) VALUES ('m0000000000000000000000003', 'bogus', 'doc', 'd1', 'grant', 'v', 'd', 'applied', 1, 's', '2026-07-14T00:00:00Z')`},
		{"mutation non-persisted outcome", `INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at) VALUES ('m0000000000000000000000004', 'resource', 'doc', 'd1', 'grant', 'v', 'd', 'semantic_conflict', 1, 's', '2026-07-14T00:00:00Z')`},
		{"mutation empty digest", `INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at) VALUES ('m0000000000000000000000005', 'resource', 'doc', 'd1', 'grant', 'v', '', 'applied', 1, 's', '2026-07-14T00:00:00Z')`},
		{"relationship empty relation", `INSERT INTO iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, created_at) VALUES ('doc', 'd', '', 'user', 'u', '2026-07-14T00:00:00Z')`},
		{"role half-populated scope pair", `INSERT INTO iam_roles (subject_type, subject_id, role, resource_type, resource_id, created_at) VALUES ('user', 'u', 'editor', 'doc', '', '2026-07-14T00:00:00Z')`},
	}
	for _, c := range rejects {
		if _, err := db.Exec(ctx, c.sql); err == nil {
			t.Errorf("%s: expected a constraint violation, got nil", c.name)
		}
	}
}
