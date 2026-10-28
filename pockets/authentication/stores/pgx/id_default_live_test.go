package pgx

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestRecordIDBackstop_Live proves the record-class backstop (plan D3): a raw
// SQL insert that omits the id gets a UUIDv7 from the column default, for every
// record-class table in generatedIDColumns.
func TestRecordIDBackstop_Live(t *testing.T) {
	db := probeDial(t)
	probeResetSchema(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })
	ctx := context.Background()
	q := qualified{schema: testSchema(t)}

	now := time.Now().UTC()
	inserts := map[string]struct {
		sql  string
		args []any
	}{
		"api_keys":              {`(service_account_id, key_hash, created_at) VALUES ('sa-raw', 'hash-raw', $1)`, []any{now}},
		"security_events":       {`(event_type, event_status, created_at) VALUES ('login', 'success', $1)`, []any{now}},
		"user_identifiers":      {`(user_id, kind, normalized_value, created_at, updated_at) VALUES ('u-raw', 'email', 'raw@example.com', $1, $1)`, []any{now}},
		"challenges":            {`(user_id, purpose, secret_digest, expires_at, created_at) VALUES ('u-raw', 'raw', 'digest-raw', $1, $1)`, []any{now}},
		"contact_changes":       {`(user_id, kind, new_value, expires_at, created_at) VALUES ('u-raw', 'email', 'raw@example.com', $1, $1)`, []any{now}},
		"authentication_grants": {`(session_id, user_id, purpose, context_digest, authenticated_at, expires_at, created_at) VALUES ('s-raw', 'u-raw', 'raw', 'ctx-raw', $1, $1, $1)`, []any{now}},
		"passkey_credentials":   {`(user_id, name, credential_id, public_key, user_handle, created_at) VALUES ('u-raw', 'raw', '\x01'::bytea, '\x02'::bytea, '\x03'::bytea, $1)`, []any{now}},
		"recovery_codes":        {`(user_id, key_id, code_digest, generated_at) VALUES ('u-raw', 'k1', 'digest-raw', $1)`, []any{now}},
	}
	for _, g := range generatedIDColumns {
		if g.Backstop != recordBackstop {
			continue
		}
		t.Run(g.Table, func(t *testing.T) {
			ins, ok := inserts[g.Table]
			if !ok {
				t.Fatalf("no raw insert for record-class table %s", g.Table)
			}
			var id string
			if err := db.QueryRow(ctx, `INSERT INTO `+q.table(g.Table)+` `+ins.sql+` RETURNING id`, ins.args...).Scan(&id); err != nil {
				t.Fatalf("raw insert: %v", err)
			}
			assertUUIDv7(t, g.Table, id)
		})
	}
}

// TestRecoveryCodeStoreBackstop_Live proves the one store SQL that mints
// (totp.go replaceCodes): a recovery code reaching the store with an empty id
// gets a UUIDv7.
func TestRecoveryCodeStoreBackstop_Live(t *testing.T) {
	db := probeDial(t)
	probeResetSchema(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })
	ctx := context.Background()
	schema := testSchema(t)
	repos, err := Repositories(ctx, db, storeOpts(schema)...)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	dbIDs := sdk.NewIDGenerator(sdk.DatabaseID)
	ident, err := identifier.New(dbIDs, identifier.DefaultNormalizer{}, "", identifier.KindEmail, "backstop@example.com", identifier.Uses{Recovery: true, Notification: true}, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, user.NewUser(dbIDs, "Backstop", now), ident)
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(10 * time.Minute)
	pending := totp.Credential{UserID: u.ID, Secret: "v1.k1.sealed", SessionID: "session-backstop", Tier: "aal2", AuthRevision: u.AuthRevision, ExpiresAt: &expires, CreatedAt: now}
	if err := repos.TOTP.Begin(ctx, pending); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	codes := []totp.RecoveryCode{
		{UserID: u.ID, KeyID: "k1", Digest: "digest-1", GeneratedAt: now},
		{UserID: u.ID, KeyID: "k1", Digest: "digest-2", GeneratedAt: now},
	}
	if err := repos.TOTP.Confirm(ctx, totp.Confirmation{UserID: u.ID, ExpectedSecret: pending.Secret, ExpectedSessionID: pending.SessionID, ExpectedAuthRevision: u.AuthRevision, Step: 1, ConfirmedAt: now, Codes: codes}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	rows, err := db.Query(ctx, `SELECT id FROM `+qualified{schema: schema}.table(recoveryCodesTable)+` WHERE user_id = $1`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		assertUUIDv7(t, recoveryCodesTable, id)
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(codes) {
		t.Fatalf("stored %d recovery codes, want %d", n, len(codes))
	}
}

// assertUUIDv7 fails unless id is a canonical lowercase UUID with version 7.
func assertUUIDv7(t *testing.T, table, id string) {
	t.Helper()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("%s id %q is not a canonical UUID", table, id)
	}
	if id[14] != '7' {
		t.Fatalf("%s id %q has version nibble %q, want 7", table, id, id[14])
	}
	if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("%s id %q has variant nibble %q, want RFC 9562 (8, 9, a or b)", table, id, v)
	}
}
