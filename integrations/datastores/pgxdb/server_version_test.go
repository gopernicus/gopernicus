package pgxdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// versionQuerier answers serverVersionQuery with a fixed result or error.
type versionQuerier struct {
	num      int
	version  string
	err      error
	gotQuery string
}

func (q *versionQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("versionQuerier: Exec not supported")
}

func (q *versionQuerier) Query(context.Context, string, ...any) (jackpgx.Rows, error) {
	return nil, errors.New("versionQuerier: Query not supported")
}

func (q *versionQuerier) QueryRow(_ context.Context, query string, _ ...any) jackpgx.Row {
	q.gotQuery = query
	return versionRow{q: q}
}

type versionRow struct{ q *versionQuerier }

func (r versionRow) Scan(dest ...any) error {
	if r.q.err != nil {
		return r.q.err
	}
	*dest[0].(*int) = r.q.num
	*dest[1].(*string) = r.q.version
	return nil
}

func jsonRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestReadServerVersion_ScansBothForms(t *testing.T) {
	q := &versionQuerier{num: 180001, version: "18.1 (Debian 18.1-1.pgdg13+2)"}
	num, version, err := readServerVersion(context.Background(), q)
	if err != nil {
		t.Fatalf("readServerVersion: %v", err)
	}
	if num != 180001 || version != "18.1 (Debian 18.1-1.pgdg13+2)" {
		t.Fatalf("got %d %q", num, version)
	}
	if q.gotQuery != serverVersionQuery {
		t.Fatalf("query = %q, want %q", q.gotQuery, serverVersionQuery)
	}
}

func TestCheckServerVersion_RefusesBelowFloor(t *testing.T) {
	err := checkServerVersion(170004, "17.4 (Homebrew)")
	if !errors.Is(err, ErrServerVersion) {
		t.Fatalf("err = %v, want ErrServerVersion", err)
	}
	want := `pgxdb: PostgreSQL 18 or later is required: server reports "17.4 (Homebrew)" (server_version_num 170004 < 180000); ` +
		`the canonical pgx migrations use uuidv7() (PostgreSQL 18). Upgrade the server, or hold pgxdb at v0.16.x and the pgx stores at their previous tags`
	if !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err text =\n%s\nwant prefix\n%s", err, want)
	}
}

func TestCheckServerVersion_AcceptsFloorAndAbove(t *testing.T) {
	for _, num := range []int{MinServerVersionNum, 180004, 190000} {
		if err := checkServerVersion(num, "18.x"); err != nil {
			t.Fatalf("checkServerVersion(%d) = %v, want nil", num, err)
		}
	}
}

func TestAdviseServerVersion_WarnsOnceBelowFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	adviseServerVersion(context.Background(), &versionQuerier{num: 170004, version: "17.4 (Homebrew)"}, logger, "", "")

	records := jsonRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), buf.String())
	}
	rec := records[0]
	if rec["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", rec["level"])
	}
	if rec["msg"] != "pgxdb: server below the PostgreSQL 18 floor; RunMigrations will refuse" {
		t.Fatalf("msg = %v", rec["msg"])
	}
	if rec["server_version_num"] != float64(170004) || rec["required"] != float64(MinServerVersionNum) || rec["server_version"] != "17.4 (Homebrew)" {
		t.Fatalf("attrs = %v", rec)
	}
}

func TestAdviseServerVersion_SilentAtFloor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	adviseServerVersion(context.Background(), &versionQuerier{num: MinServerVersionNum, version: "18.0"}, logger, "", "")
	if buf.Len() != 0 {
		t.Fatalf("logged at the floor: %s", buf.String())
	}
}

func TestAdviseServerVersion_ProbeFailureWarnsRedacted(t *testing.T) {
	const (
		dsn      = "postgres://app:hunter2@db.internal:5432/app?sslmode=verify-full"
		password = "hunter2"
	)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	q := &versionQuerier{err: errors.New("probe failed against " + dsn + " (password " + password + ")")}
	adviseServerVersion(context.Background(), q, logger, dsn, password)

	records := jsonRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), buf.String())
	}
	if records[0]["level"] != "WARN" || records[0]["required"] != float64(MinServerVersionNum) {
		t.Fatalf("record = %v", records[0])
	}
	if strings.Contains(buf.String(), password) {
		t.Fatalf("password leaked into the advisory: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "probe failed") {
		t.Fatalf("error text missing from the advisory: %s", buf.String())
	}
}

func TestAdviseServerVersion_NilLoggerUsesDefault(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	adviseServerVersion(context.Background(), &versionQuerier{num: 160000, version: "16.0"}, nil, "", "")
	if n := len(jsonRecords(t, &buf)); n != 1 {
		t.Fatalf("default logger records = %d, want 1: %s", n, buf.String())
	}
}
