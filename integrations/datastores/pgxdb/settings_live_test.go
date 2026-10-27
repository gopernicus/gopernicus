package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
)

const settingsProbe = "app.settings_probe"

// openSettingsDB opens a *DB on POSTGRES_TEST_DSN with extra DSN query
// parameters, skipping loudly without the DSN.
func openSettingsDB(t *testing.T, maxConns int, params url.Values) *DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — TransactWithSettings NOT verified")
	}
	if len(params) > 0 {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("parse POSTGRES_TEST_DSN: %v", err)
		}
		q := u.Query()
		for k, vs := range params {
			for _, v := range vs {
				q.Set(k, v)
			}
		}
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn), MaxConns: maxConns})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func backendPID(t *testing.T, ctx context.Context, q Querier) int {
	t.Helper()
	var pid int
	if err := q.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	return pid
}

// TestLive_TransactWithSettings_DoesNotLeak runs on a one-connection pool, so
// every statement inside and after fn is on the same physical connection
// (asserted by pg_backend_pid). Inside fn only the transaction is queried:
// with MaxConns 1, a pool query from inside fn waits for the connection fn
// holds and deadlocks.
func TestLive_TransactWithSettings_DoesNotLeak(t *testing.T) {
	db := openSettingsDB(t, 1, nil)
	ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
	defer cancel()
	refused := errors.New("callback refused")

	for _, outcome := range []string{"commit", "error rollback", "recovered panic"} {
		t.Run(outcome, func(t *testing.T) {
			value := fmt.Sprintf("v-%d", time.Now().UnixNano())
			var insidePID int
			run := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						if r != "boom" {
							panic(r)
						}
						err = refused
					}
				}()
				return db.TransactWithSettings(ctx, []Setting{{Name: settingsProbe, Value: value}}, func(ctx context.Context) error {
					tx, ok := db.TxFrom(ctx)
					if !ok {
						t.Fatal("db.TxFrom found no transaction inside TransactWithSettings")
					}
					insidePID = backendPID(t, ctx, tx)
					var got string
					if err := tx.QueryRow(ctx, "SELECT current_setting($1)", settingsProbe).Scan(&got); err != nil {
						t.Fatalf("current_setting inside: %v", err)
					}
					if got != value {
						t.Fatalf("current_setting inside = %q, want %q", got, value)
					}
					if v, ok := tx.Setting(settingsProbe); !ok || v != value {
						t.Fatalf("tx.Setting = (%q, %t), want (%q, true)", v, ok, value)
					}
					if err := db.TransactWithSettings(ctx, []Setting{{Name: settingsProbe, Value: "nested"}}, func(context.Context) error { return nil }); !errors.Is(err, ErrNestedTransact) {
						t.Fatalf("nested TransactWithSettings = %v, want ErrNestedTransact", err)
					}
					if err := db.Transact(ctx, func(context.Context) error { return nil }); !errors.Is(err, ErrNestedTransact) {
						t.Fatalf("nested Transact = %v, want ErrNestedTransact", err)
					}
					switch outcome {
					case "error rollback":
						return refused
					case "recovered panic":
						panic("boom")
					}
					return nil
				})
			}
			err := run()
			if outcome == "commit" && err != nil {
				t.Fatalf("TransactWithSettings: %v", err)
			}
			if outcome != "commit" && err != refused {
				t.Fatalf("TransactWithSettings = %v, want the callback's refusal", err)
			}

			if after := backendPID(t, ctx, db); after != insidePID {
				t.Fatalf("pg_backend_pid after = %d, inside = %d: not the same connection", after, insidePID)
			}
			var after *string
			if err := db.QueryRow(ctx, "SELECT current_setting($1, true)", settingsProbe).Scan(&after); err != nil {
				t.Fatalf("current_setting after: %v", err)
			}
			if after != nil && *after != "" {
				t.Fatalf("current_setting after %s = %q, want '' or NULL (the setting leaked)", outcome, *after)
			}
		})
	}

	t.Run("Transact records no settings", func(t *testing.T) {
		if err := db.Transact(ctx, func(ctx context.Context) error {
			tx, _ := db.TxFrom(ctx)
			if v, ok := tx.Setting(settingsProbe); ok {
				t.Fatalf("tx.Setting on a Transact transaction = (%q, true), want absent", v)
			}
			return nil
		}); err != nil {
			t.Fatalf("Transact: %v", err)
		}
	})
}

// TestLive_TransactWithSettings_BoundValues round-trips values that would break
// a concatenated statement, under the default exec mode and simple protocol.
func TestLive_TransactWithSettings_BoundValues(t *testing.T) {
	values := []string{"'", ";", "--", "a'; SELECT 1; --", `\'`, "tenant-ü-日本-🙂"}
	for mode, params := range map[string]url.Values{
		"default":         nil,
		"simple_protocol": {"default_query_exec_mode": {"simple_protocol"}},
	} {
		t.Run(mode, func(t *testing.T) {
			db := openSettingsDB(t, 0, params)
			ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
			defer cancel()
			for _, value := range values {
				settings := []Setting{{Name: settingsProbe, Value: value}, {Name: "app.second_probe", Value: value + "2"}}
				if err := db.TransactWithSettings(ctx, settings, func(ctx context.Context) error {
					tx, _ := db.TxFrom(ctx)
					var first, second string
					if err := tx.QueryRow(ctx, "SELECT current_setting('app.settings_probe'), current_setting('app.second_probe')").Scan(&first, &second); err != nil {
						return err
					}
					if first != value || second != value+"2" {
						t.Errorf("round-trip of %q = (%q, %q), want (%q, %q)", value, first, second, value, value+"2")
					}
					return nil
				}); err != nil {
					t.Fatalf("TransactWithSettings(%q): %v", value, err)
				}
			}
		})
	}
}

// TestLive_TransactWithSettings_RowLevelSecurity proves the RLS shape the guide
// documents: a FORCE RLS table, a NULLIF(current_setting(...)) policy and a
// non-owner, non-superuser role entered with SET LOCAL ROLE as fn's first
// statement. It needs a superuser DSN to create the role and skips loudly
// otherwise. SET LOCAL ROLE ends with the transaction; a plain SET ROLE would
// leak the role to the next borrower.
func TestLive_TransactWithSettings_RowLevelSecurity(t *testing.T) {
	db := openSettingsDB(t, 0, nil)
	ctx, cancel := context.WithTimeout(t.Context(), instanceTimeout)
	defer cancel()

	var super bool
	if err := db.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&super); err != nil {
		t.Fatalf("read rolsuper: %v", err)
	}
	if !super {
		t.Skip("POSTGRES_TEST_DSN is not a superuser — row-level security behavior NOT verified")
	}

	suffix := time.Now().UnixNano()
	schema := fmt.Sprintf("rls_probe_%d", suffix)
	role := fmt.Sprintf("rls_probe_role_%d", suffix)
	table := schema + ".notes"
	t.Cleanup(func() {
		for _, stmt := range []string{
			"DROP SCHEMA IF EXISTS " + schema + " CASCADE",
			"DROP OWNED BY " + role,
			"DROP ROLE IF EXISTS " + role,
		} {
			if _, err := db.Exec(context.Background(), stmt); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})

	const policy = "tenant_id = NULLIF(current_setting('app.tenant_id', true), '')"
	for _, stmt := range []string{
		"CREATE ROLE " + role + " NOLOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT",
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + table + " (tenant_id text NOT NULL, id text NOT NULL, PRIMARY KEY (tenant_id, id))",
		"INSERT INTO " + table + " (tenant_id, id) VALUES ('a', 'a1'), ('a', 'a2'), ('b', 'b1')",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"ALTER TABLE " + table + " FORCE ROW LEVEL SECURITY",
		"CREATE POLICY tenant_isolation ON " + table + " USING (" + policy + ") WITH CHECK (" + policy + ")",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON " + table + " TO " + role,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	tenantA := []Setting{{Name: "app.tenant_id", Value: "a"}}
	asRole := func(ctx context.Context) Querier {
		tx, _ := db.TxFrom(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			t.Fatalf("SET LOCAL ROLE: %v", err)
		}
		return tx
	}
	visible := func(ctx context.Context, q Querier) []string {
		rows, err := q.Query(ctx, "SELECT tenant_id || '/' || id FROM "+table+" ORDER BY 1")
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return got
	}
	onlyA := func(t *testing.T, got []string) {
		t.Helper()
		if len(got) != 2 || got[0] != "a/a1" || got[1] != "a/a2" {
			t.Fatalf("visible rows = %v, want [a/a1 a/a2]", got)
		}
	}
	sessionRoleRestored := func(t *testing.T) {
		t.Helper()
		var current, session string
		if err := db.QueryRow(ctx, "SELECT current_user, session_user").Scan(&current, &session); err != nil {
			t.Fatalf("current_user: %v", err)
		}
		if current != session {
			t.Fatalf("after commit current_user = %q, session_user = %q: the role leaked", current, session)
		}
	}

	t.Run("tenant sees only its rows", func(t *testing.T) {
		var got []string
		if err := db.TransactWithSettings(ctx, tenantA, func(ctx context.Context) error {
			got = visible(ctx, asRole(ctx))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		onlyA(t, got)
		sessionRoleRestored(t)
	})

	t.Run("insert for another tenant is forbidden", func(t *testing.T) {
		err := db.TransactWithSettings(ctx, tenantA, func(ctx context.Context) error {
			_, err := asRole(ctx).Exec(ctx, "INSERT INTO "+table+" (tenant_id, id) VALUES ('b', 'b2')")
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.Is(err, sdk.ErrForbidden) || !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("insert of b's row = %v, want sdk.ErrForbidden with SQLSTATE 42501", err)
		}
		sessionRoleRestored(t)
	})

	t.Run("update of another tenant's row affects nothing", func(t *testing.T) {
		if err := db.TransactWithSettings(ctx, tenantA, func(ctx context.Context) error {
			tag, err := asRole(ctx).Exec(ctx, "UPDATE "+table+" SET id = 'b9' WHERE tenant_id = 'b' AND id = 'b1'")
			if err != nil {
				return err
			}
			if n := tag.RowsAffected(); n != 0 {
				t.Fatalf("update of b's row affected %d rows, want 0", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sessionRoleRestored(t)
	})

	t.Run("plain Transact fails closed", func(t *testing.T) {
		var got []string
		if err := db.Transact(ctx, func(ctx context.Context) error {
			got = visible(ctx, asRole(ctx))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("plain Transact sees %v, want no rows", got)
		}
		sessionRoleRestored(t)
	})

	t.Run("FORCE binds the owner", func(t *testing.T) {
		if _, err := db.Exec(ctx, "ALTER TABLE "+table+" OWNER TO "+role); err != nil {
			t.Fatalf("transfer ownership: %v", err)
		}
		var got []string
		if err := db.TransactWithSettings(ctx, tenantA, func(ctx context.Context) error {
			got = visible(ctx, asRole(ctx))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		onlyA(t, got)
		sessionRoleRestored(t)
	})
}
