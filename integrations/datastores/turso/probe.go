package turso

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

const (
	// appendOnlyMessage ends the RAISE(ABORT) message of every immutability
	// trigger, "<table> is append-only"; MapError matches it.
	appendOnlyMessage = "is append-only"

	inspectTriggersSQL = `SELECT name, sql FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = ?`
)

// AppendOnlyTable is one append-only table a store owns and the immutability
// triggers its migrations create on it.
type AppendOnlyTable struct {
	Name     string
	Triggers []TableTrigger
}

// TableTrigger is one immutability trigger, by name, and the CREATE TRIGGER
// statements the store ships for it; the trigger is intact when its stored
// definition equals one of them.
type TableTrigger struct {
	Name      string
	Canonical []TriggerBody
}

// TriggerBody is one canonical CREATE TRIGGER statement as sqlite_schema stores
// it: the migration file's text, compared without surrounding whitespace or a
// trailing semicolon. SQLite also drops IF NOT EXISTS and a main. qualifier, so
// a statement written with either must be passed without it.
type TriggerBody struct {
	SQL string
}

// ProbeAuditImmutable reports, under the RuntimeMode the DB was opened with,
// every table whose immutability triggers are absent or differ from the
// canonical text: one WARN per such table naming the first failing trigger and
// the reason (absent or differs). SQLite has no privileges to probe, so it never
// refuses; a failing query is returned. The zero mode returns nil without a
// query.
func (d *DB) ProbeAuditImmutable(ctx context.Context, tables ...AppendOnlyTable) error {
	if d.mode == "" || len(tables) == 0 {
		return nil
	}
	logger := d.logger
	if logger == nil {
		logger = slog.Default()
	}
	for _, table := range tables {
		trigger, reason, err := d.immutableTriggerFinding(ctx, table)
		if err != nil {
			return fmt.Errorf("turso: audit trigger probe on %s: %w", table.Name, err)
		}
		if reason == "" {
			continue
		}
		logger.Warn("turso: audit table has no immutability triggers; restore the migration file",
			slog.String("table", table.Name), slog.String("trigger", trigger), slog.String("reason", reason))
	}
	return nil
}

// immutableTriggerFinding returns the first of table's triggers that is absent
// or whose stored definition matches no canonical statement, with the reason;
// both are empty when every trigger is intact.
func (d *DB) immutableTriggerFinding(ctx context.Context, table AppendOnlyTable) (trigger, reason string, err error) {
	rows, err := d.Query(ctx, inspectTriggersSQL, table.Name)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	stored := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return "", "", MapError(err)
		}
		stored[name] = strings.TrimSpace(definition)
	}
	if err := rows.Err(); err != nil {
		return "", "", MapError(err)
	}
	for _, tr := range table.Triggers {
		definition, ok := stored[tr.Name]
		if !ok {
			return tr.Name, "absent", nil
		}
		if !canonicalTrigger(definition, tr.Canonical) {
			return tr.Name, "differs", nil
		}
	}
	return "", "", nil
}

// canonicalTrigger reports whether the stored definition equals one of the
// canonical statements.
func canonicalTrigger(definition string, canonical []TriggerBody) bool {
	for _, c := range canonical {
		if definition == strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(c.SQL), ";")) {
			return true
		}
	}
	return false
}
