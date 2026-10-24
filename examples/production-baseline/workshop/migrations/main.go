// Command migrations is the host-owned, pre-boot migration runner for
// examples/production-baseline. The framework never migrates at startup; this
// runner applies the host-owned ledger (workshop/migrations/primary) before
// the server boots: the authentication and authorization pgx stores' exported
// migrations and the pgxdb rate limiter's reference DDL, as one ordered stream
// under one schema_migrations ledger.
//
// Usage (APP_RUNTIME_MODE comes from the process environment; `make migrate`
// exports development):
//
//	go run ./workshop/migrations           apply pending migrations
//	go run ./workshop/migrations -status   report applied vs pending (file-only
//	                                       when the database is unreachable)
//	go run ./workshop/migrations -posture-role <runtime role>
//	                                       preflight the runtime role's
//	                                       append-only posture before deploy
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	authenticationpgx "github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx"
	authorizationpgx "github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx"
)

// migrationsFS embeds the host's own copy of the migrations. The runner applies
// *.sql files in filename order and skips names prefixed with "_".
//
//go:embed primary
var migrationsFS embed.FS

const migrationsDir = "primary"

// appendOnlyWritableAcknowledged is the literal the server passes as
// pgxdb.Config.AppendOnlyWritableAcknowledged, so -posture-role decides as the
// stores' boot probe will. The server passes none.
const appendOnlyWritableAcknowledged = false

func main() {
	status := flag.Bool("status", false, "report applied vs pending migrations and exit")
	postureRole := flag.String("posture-role", "", "preflight this runtime role's append-only posture and exit")
	flag.Parse()

	// The runner holds the most privileged connection, so it resolves the mode
	// like the server: from the process environment only, reading .env in
	// development and never in production.
	mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")
	if err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}

	logOpts := logging.Options{Format: "text", Output: "STDOUT"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	log := logging.New(logOpts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx, log, mode, *status, *postureRole); err != nil {
		log.Error("migrations", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, mode environment.Mode, status bool, postureRole string) error {
	if postureRole != "" {
		return reportPosture(ctx, mode, postureRole)
	}
	db, err := open(ctx, mode)
	if status {
		return reportStatus(ctx, db, err)
	}
	if err != nil {
		return err
	}
	defer db.Close()

	log.Info("running migrations", "dir", migrationsDir)
	if err := pgxdb.RunMigrations(ctx, db, migrationsFS, migrationsDir); err != nil {
		return err
	}
	log.Info("migrations complete")
	return nil
}

// open connects under mode: production refuses a connection without verified
// TLS, as the server does.
func open(ctx context.Context, mode environment.Mode) (*pgxdb.DB, error) {
	var cfg pgxdb.Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		return nil, err
	}
	cfg.RuntimeMode = mode
	return pgxdb.Open(ctx, cfg)
}

// postureTables are the append-only audit tables -posture-role inspects: each
// mounted pgx store's AuditTables(), called with the options the server
// constructs that store with, so this preflight and the stores' boot probe
// judge the same tables.
func postureTables() []pgxdb.AppendOnlyTable {
	tables := authenticationpgx.AuditTables()
	return append(tables, authorizationpgx.AuditTables(authorizationpgx.WithAudit())...)
}

// reportPosture preflights role — the runtime role the server connects as —
// from this runner's migrator connection: one line per append-only table,
// decided exactly as that role's boot probe in production decides, and for a
// table with definer functions a functions= line with the rail's database
// floor. It fails when a table is unprotected, the role cannot append to it,
// or a definer function is missing or unsafe (the boot refuses each); a
// missing EXECUTE or an inoperative function is reported and does not fail —
// the boot WARNs on both. Archive its output with the deploy.
func reportPosture(ctx context.Context, mode environment.Mode, role string) error {
	tables := postureTables()

	db, err := open(ctx, mode)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	report, err := pgxdb.InspectTablePrivileges(ctx, tx, role, tables...)
	if err != nil {
		return err
	}
	failing := 0
	for _, t := range report.Tables {
		protectedBy := t.ProtectedBy(appendOnlyWritableAcknowledged)
		trigger := t.TriggerState
		if trigger == "" {
			trigger = "absent"
		}
		fmt.Printf("table=%s protected_by=%s owner=%t insert=%t sequence_usage=%t trigger=%s\n",
			t.Table, protectedBy, t.Owner, t.Insert, t.SequenceUsage, trigger)
		if protectedBy == "none" || t.CannotAppend() {
			failing++
		}
		if len(t.Functions) == 0 {
			continue
		}
		functions, broken := describeFunctions(t)
		floor, err := railFloor(ctx, tx)
		if err != nil {
			return err
		}
		fmt.Printf("functions=[%s] floor=%s\n", functions, floor)
		failing += broken
	}
	if failing > 0 {
		return fmt.Errorf("posture: role %s: %d findings over %d append-only tables (unprotected, not appendable, or a missing or unsafe definer function)", report.Role, failing, len(report.Tables))
	}
	return nil
}

// describeFunctions renders each definer function of t as
// name:execute=… unsafe=… inoperative=… and counts the ones the boot would
// refuse: missing or unsafe.
func describeFunctions(t pgxdb.TablePrivilege) (string, int) {
	signatures := make([]string, 0, len(t.Functions))
	for signature := range t.Functions {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	var parts []string
	broken := 0
	for _, signature := range signatures {
		f := t.Functions[signature]
		name := signature
		if i := strings.Index(name, "("); i >= 0 {
			name = name[:i]
		}
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if !f.Exists {
			parts = append(parts, name+":missing")
			broken++
			continue
		}
		unsafe := reasons(map[string]bool{"public_execute": f.PublicExecute, "search_path": !f.SearchPathPinned, "namespace": !f.SameNamespace})
		inoperative := reasons(map[string]bool{"invoker": !f.SecurityDefiner, "owner": !f.OwnerIsTableOwner, "superuser_owner": f.OwnerSuperuser, "schema_create": f.PathSchemaCreate})
		if unsafe != "none" {
			broken++
		}
		parts = append(parts, fmt.Sprintf("%s:execute=%t unsafe=%s inoperative=%s", name, f.Execute, unsafe, inoperative))
	}
	return strings.Join(parts, " "), broken
}

// reasons names the set flags, sorted and comma-joined, or "none".
func reasons(flags map[string]bool) string {
	var set []string
	for name, on := range flags {
		if on {
			set = append(set, name)
		}
	}
	if len(set) == 0 {
		return "none"
	}
	sort.Strings(set)
	return strings.Join(set, ",")
}

// railFloor reads the rail's database retention floor from migration 0024's
// floor function, in whole days; "missing" when the function is absent.
func railFloor(ctx context.Context, q pgxdb.Querier) (string, error) {
	var present bool
	if err := q.QueryRow(ctx, `SELECT pg_catalog.to_regprocedure('security_events_retention_floor()') IS NOT NULL`).Scan(&present); err != nil {
		return "", err
	}
	if !present {
		return "missing", nil
	}
	var days int
	if err := q.QueryRow(ctx, `SELECT pg_catalog.date_part('day', security_events_retention_floor())::int`).Scan(&days); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d days", days), nil
}

// reportStatus prints applied vs pending migrations. When the database is
// unreachable (open failed, or the readiness check fails) it falls back to a
// file-only listing: every embedded migration is reported pending. This is the
// only status a runner can self-produce without a live database.
func reportStatus(ctx context.Context, db *pgxdb.DB, openErr error) error {
	files, err := migrationFiles()
	if err != nil {
		return err
	}

	if openErr != nil || pgxdb.StatusCheck(ctx, db) != nil {
		if db != nil {
			db.Close()
		}
		fmt.Println("database unreachable — reporting from migration files only")
		printList("pending", files)
		return nil
	}
	defer db.Close()

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	var appliedList, pending []string
	for _, f := range files {
		if _, ok := applied[f]; ok {
			appliedList = append(appliedList, f)
		} else {
			pending = append(pending, f)
		}
	}
	printList("applied", appliedList)
	printList("pending", pending)
	return nil
}

// appliedVersions reads the connector's portable ledger. The (source, version,
// checksum) shape is identical across the supported dialects, so this SELECT is
// dialect-agnostic; version is the migration filename.
func appliedVersions(ctx context.Context, db *pgxdb.DB) (map[string]string, error) {
	rows, err := db.Query(ctx, "SELECT source, version, checksum FROM schema_migrations ORDER BY source, version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var source, version, checksum string
		if err := rows.Scan(&source, &version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = source
	}
	return applied, rows.Err()
}

// migrationFiles lists the embedded *.sql migrations in filename order, skipping
// "_"-prefixed files (fs.ReadDir returns entries already sorted by name).
func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, "_") || !strings.HasSuffix(name, ".sql") {
			continue
		}
		files = append(files, name)
	}
	return files, nil
}

func printList(label string, files []string) {
	fmt.Printf("%s (%d):\n", label, len(files))
	for _, f := range files {
		fmt.Printf("  %s\n", f)
	}
}
