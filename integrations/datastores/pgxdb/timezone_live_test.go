package pgxdb

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
)

// timestampsSQL selects the three shapes D6a covers: a server-minted instant, a
// literal with a non-UTC offset, and a timestamptz[] (the array codec case).
const timestampsSQL = `SELECT now() AS now_at, '2026-01-01T00:00:00+05:00'::timestamptz AS fixed_at, ARRAY[now()]::timestamptz[] AS stamps`

// fixedInstant is what the literal in timestampsSQL means, in UTC.
var fixedInstant = time.Date(2025, 12, 31, 19, 0, 0, 0, time.UTC)

// timestampsRow is the struct-scan destination for the QueryOne leg.
type timestampsRow struct {
	NowAt   time.Time   `db:"now_at"`
	FixedAt time.Time   `db:"fixed_at"`
	Stamps  []time.Time `db:"stamps"`
}

// TestLive_ScanUTC is D6's proof against a real server: every scanned
// timestamptz — scalar, array, extended protocol (binary decoder), simple
// protocol (text decoder), row scan and struct scan — comes back located in UTC
// and marshals with a Z; the session time zone defaults to UTC; and a host that
// names a zone in its DSN keeps it while its scans stay UTC-located.
//
// It skips loudly when POSTGRES_TEST_DSN is unset — a silent green here would be
// a false green. Spin a throwaway database with:
//
//	docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
//	POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test -run TestLive_ ./...
func TestLive_ScanUTC(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — postgres time-zone conformance NOT verified")
	}

	ctx := context.Background()

	db, err := Open(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The default session zone (D6b): the DSN names none, so the connector does.
	var zone string
	if err := db.QueryRow(ctx, "SHOW TimeZone").Scan(&zone); err != nil {
		t.Fatalf("show timezone: %v", err)
	}
	if zone != "UTC" {
		t.Fatalf("SHOW TimeZone = %q, want %q", zone, "UTC")
	}
	t.Logf("SHOW TimeZone = %q", zone)

	// Extended protocol (pgx's default) — the binary decoder, the one that used
	// to hand back time.Local values.
	t.Run("row_scan_extended", func(t *testing.T) {
		var row timestampsRow
		if err := db.QueryRow(ctx, timestampsSQL).Scan(&row.NowAt, &row.FixedAt, &row.Stamps); err != nil {
			t.Fatalf("scan: %v", err)
		}
		assertUTCRow(t, row)
	})

	// Simple protocol — the text decoder. Same ScanLocation seam.
	t.Run("row_scan_simple_protocol", func(t *testing.T) {
		var row timestampsRow
		if err := db.QueryRow(ctx, timestampsSQL, jackpgx.QueryExecModeSimpleProtocol).Scan(&row.NowAt, &row.FixedAt, &row.Stamps); err != nil {
			t.Fatalf("scan: %v", err)
		}
		assertUTCRow(t, row)
	})

	// The struct-scan path every pgx store actually uses (RowToStructByName).
	t.Run("struct_scan_queryone", func(t *testing.T) {
		row, err := QueryOne[timestampsRow](ctx, db, timestampsSQL, nil)
		if err != nil {
			t.Fatalf("QueryOne: %v", err)
		}
		assertUTCRow(t, row)
	})

	// A host that names its own session zone keeps it (D6b's escape hatch) while
	// scans stay UTC-located (D6a is unconditional).
	t.Run("host_named_zone_wins", func(t *testing.T) {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("parse POSTGRES_TEST_DSN: %v", err)
		}
		q := u.Query()
		q.Set("timezone", "Europe/Oslo")
		u.RawQuery = q.Encode()

		oslo, err := Open(Config{DSN: u.String()})
		if err != nil {
			t.Fatalf("open with timezone=Europe/Oslo: %v", err)
		}
		t.Cleanup(func() { _ = oslo.Close() })

		var zone string
		if err := oslo.QueryRow(ctx, "SHOW TimeZone").Scan(&zone); err != nil {
			t.Fatalf("show timezone: %v", err)
		}
		if zone != "Europe/Oslo" {
			t.Fatalf("SHOW TimeZone = %q, want %q — the host's DSN choice must win", zone, "Europe/Oslo")
		}
		t.Logf("host-named session zone: SHOW TimeZone = %q", zone)

		var row timestampsRow
		if err := oslo.QueryRow(ctx, timestampsSQL).Scan(&row.NowAt, &row.FixedAt, &row.Stamps); err != nil {
			t.Fatalf("scan: %v", err)
		}
		assertUTCRow(t, row)
	})
}

// assertUTCRow is the shared D6a assertion: UTC-located scalars and array
// elements, the literal's instant preserved, and Z on the JSON wire.
func assertUTCRow(t *testing.T, row timestampsRow) {
	t.Helper()

	if row.NowAt.Location() != time.UTC {
		t.Fatalf("now() location = %v, want time.UTC (value %v)", row.NowAt.Location(), row.NowAt)
	}
	if row.FixedAt.Location() != time.UTC {
		t.Fatalf("literal location = %v, want time.UTC (value %v)", row.FixedAt.Location(), row.FixedAt)
	}
	if !row.FixedAt.Equal(fixedInstant) {
		t.Fatalf("literal = %v, want the instant %v", row.FixedAt, fixedInstant)
	}
	if len(row.Stamps) != 1 {
		t.Fatalf("timestamptz[] len = %d, want 1", len(row.Stamps))
	}
	if row.Stamps[0].Location() != time.UTC {
		t.Fatalf("timestamptz[] element location = %v, want time.UTC (value %v)", row.Stamps[0].Location(), row.Stamps[0])
	}

	for name, v := range map[string]time.Time{"now_at": row.NowAt, "fixed_at": row.FixedAt, "stamps[0]": row.Stamps[0]} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if !strings.HasSuffix(string(b), `Z"`) {
			t.Fatalf("json.Marshal(%s) = %s, want a Z-suffixed RFC 3339 instant", name, b)
		}
	}

	t.Logf("now_at=%v fixed_at=%v stamps[0]=%v", row.NowAt, row.FixedAt, row.Stamps[0])
}
