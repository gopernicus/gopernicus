package turso

import (
	"errors"
	"strings"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The code-authentication (0030) store legs, untagged so `make check` runs
// them against a local SQLite file. The conformance group itself runs through
// TestConformance_LocalSQLite.

// TestCodeAuthenticationNeverBlanketProbed pins 0030 out of Repositories'
// blanket column probe: the columns are required only when the host sets
// OAuth2Config.DelegatedAssurance, and OAuth2Store.ProbeSchema asks then.
func TestCodeAuthenticationNeverBlanketProbed(t *testing.T) {
	for _, pc := range probeColumns {
		if pc.migration == codeAuthenticationMigration || pc.table == "oauth_authorization_codes" {
			t.Fatalf("probeColumns lists %+v; 0030 must be probed only through OAuth2Store.ProbeSchema", pc)
		}
	}
}

// TestCodeAuthenticationSchema_LocalSQLite pins the conditional rule on a
// database without 0030: Repositories constructs, the plain
// CreateCode/GetCode/RedeemCode path works without naming the columns, and
// ProbeSchema refuses with sdk.ErrNotFound naming the file. A closed
// connection is never schema-missing.
func TestCodeAuthenticationSchema_LocalSQLite(t *testing.T) {
	db, _, _ := oauthSQLite(t)
	ctx := t.Context()
	for _, column := range codeAuthenticationColumnNames {
		if _, err := db.Exec(ctx, "ALTER TABLE oauth_authorization_codes DROP COLUMN "+column); err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	repos, err := Repositories(ctx, db)
	if err != nil {
		t.Fatalf("Repositories without 0030 = %v, want it to construct (delegated assurance is opt-in)", err)
	}
	store := repos.OAuth2.(*OAuth2Store)
	err = store.ProbeSchema(ctx)
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), codeAuthenticationMigration) {
		t.Fatalf("ProbeSchema without 0030 = %v, want sdk.ErrNotFound naming %s", err, codeAuthenticationMigration)
	}
	t.Logf("unmigrated probe refused: %v", err)

	now := time.Now().UTC()
	browser := seedOAuthBrowser(t, db, "u-code-plain", now)
	code, proposed := oauthProposal(browser.UserID, "code-plain", now)
	if err := store.CreateCode(ctx, code, browser.ID); err != nil {
		t.Fatalf("plain CreateCode without 0030 = %v, want success", err)
	}
	got, err := store.GetCode(ctx, code.Hash)
	if err != nil || !sameOAuthCode(got, code) {
		t.Fatalf("plain GetCode without 0030 = %+v, %v", got, err)
	}
	if _, err := store.RedeemCode(ctx, got, proposed, now); err != nil {
		t.Fatalf("plain RedeemCode without 0030 = %v, want success", err)
	}

	db.Close()
	if err := store.ProbeSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on a closed connection = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}
}

// TestCodeAuthenticationEncoding_LocalSQLite proves the copy writes the
// approving session's columns verbatim — authenticated_at in created_at's
// fixed-width UTC TEXT encoding, the methods JSON and the level — and that a
// session recording nothing writes NULL in all three.
func TestCodeAuthenticationEncoding_LocalSQLite(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	ctx := t.Context()
	store := repos.OAuth2.(*OAuth2Store)
	now := time.Now().UTC()

	if _, err := db.Exec(ctx, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"u-enc", "u-enc", tursodb.FormatTime(now), tursodb.FormatTime(now)); err != nil {
		t.Fatal(err)
	}
	password, _ := session.DescribeMethod(session.MethodPassword)
	totp, _ := session.DescribeMethod(session.MethodTOTP)
	recorded, _ := session.NewSession("u-enc", time.Hour, now)
	recorded.RefreshTokenHash = "web-enc-recorded"
	recorded.Authentication = session.AuthenticationMetadata{AuthenticatedAt: now.Add(-time.Minute + 7), Methods: []session.AuthenticationMethod{password, totp}, Assurance: session.AssuranceAAL2}
	blank, _ := session.NewSession("u-enc", time.Hour, now)
	blank.RefreshTokenHash = "web-enc-blank"
	for _, s := range []session.Session{recorded, blank} {
		if _, err := repos.Sessions.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	for _, approver := range []session.Session{recorded, blank} {
		code, _ := oauthProposal("u-enc", "enc-"+approver.RefreshTokenHash, now)
		if err := store.CreateCodeWithAuthentication(ctx, code, approver.ID); err != nil {
			t.Fatal(err)
		}
		var sessAt, sessMethods, sessLevel any
		if err := db.QueryRow(ctx, `SELECT authenticated_at, authentication_methods, assurance_level FROM sessions WHERE id = ?`, approver.ID).
			Scan(&sessAt, &sessMethods, &sessLevel); err != nil {
			t.Fatal(err)
		}
		var codeAt, codeMethods, codeLevel any
		if err := db.QueryRow(ctx, `SELECT authenticated_at, authentication_methods, assurance_level FROM oauth_authorization_codes WHERE code_hash = ?`, code.Hash).
			Scan(&codeAt, &codeMethods, &codeLevel); err != nil {
			t.Fatal(err)
		}
		if approver.ID == blank.ID {
			if codeAt != nil || codeMethods != nil || codeLevel != nil {
				t.Fatalf("a session recording nothing wrote %v / %v / %v, want NULL in all three", codeAt, codeMethods, codeLevel)
			}
			continue
		}
		if asText(codeAt) != asText(sessAt) || asText(codeMethods) != asText(sessMethods) || asText(codeLevel) != asText(sessLevel) {
			t.Fatalf("code columns %q / %q / %q, want the session's %q / %q / %q", asText(codeAt), asText(codeMethods), asText(codeLevel), asText(sessAt), asText(sessMethods), asText(sessLevel))
		}
		if !strings.HasSuffix(asText(codeAt), "Z") || len(asText(codeAt)) != len("2006-01-02T15:04:05.000000000Z") {
			t.Fatalf("authenticated_at = %q, want the fixed-width UTC TEXT encoding", asText(codeAt))
		}
	}
}

// asText renders a scanned TEXT value (string or []byte) for comparison.
func asText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}
