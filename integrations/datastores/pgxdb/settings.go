package pgxdb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// settingName is the guardrail on Setting.Name: one lowercase dotted custom
// parameter. Undotted core parameters never match.
var settingName = regexp.MustCompile(`^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$`)

// Setting is one transaction-local configuration parameter.
//
// Name must be a lowercase, dotted custom (placeholder) parameter such as
// "app.tenant_id". Core PostgreSQL parameters (role, search_path, ...) are
// undotted and therefore unreachable. Extension parameters are dotted too
// (plpgsql.*, pg_trgm.*) and are NOT blocked, so use a prefix you own. This is
// a guardrail, not a security boundary. PostgreSQL matches names
// case-insensitively; the lowercase rule keeps a policy's
// current_setting('app.tenant_id') text identical to what was set.
//
// Value must be non-empty valid UTF-8 without NUL. pgxdb never concatenates
// it into SQL: it is sent as a parameter (pgx-escaped under simple protocol).
type Setting struct{ Name, Value string }

// TransactWithSettings is Transact whose first statement applies settings with
// set_config(name, value, true). They end with the transaction (commit, rollback
// or panic) and never reach another pool borrower. Nesting, cleanup and the
// per-instance ambient context are Transact's: it is implemented by wrapping
// d.Transact. Inside fn, d.TxFrom(ctx) returns a *Tx whose Setting reports them.
//
// Settings are validated before the transaction begins, so a refusal acquires
// no connection; every refusal wraps sdk.ErrInvalidInput.
func (d *DB) TransactWithSettings(ctx context.Context, settings []Setting, fn func(ctx context.Context) error) error {
	return d.withSettings(ctx, "TransactWithSettings", d.Transact, settings, fn)
}

// TransactSnapshotWithSettings is TransactWithSettings over TransactSnapshot: a
// read-write, repeatable-read transaction whose first statement applies
// settings. That statement takes the snapshot, so it starts when the
// transaction does. Use it for a tenant-scoped workflow that must read and
// then write under one snapshot, such as the authorization pgx store's reads,
// which refuse a READ COMMITTED ambient transaction. Validation, nesting,
// cleanup and Tx.Setting are TransactWithSettings's. Callbacks are never
// retried: the caller owns retries on serialization failures (40001), as with
// TransactSnapshot.
func (d *DB) TransactSnapshotWithSettings(ctx context.Context, settings []Setting, fn func(ctx context.Context) error) error {
	return d.withSettings(ctx, "TransactSnapshotWithSettings", d.TransactSnapshot, settings, fn)
}

// withSettings validates settings, then runs fn inside begin with set_config as
// the transaction's first statement and the applied settings recorded on the
// *Tx. caller names the public function in a refusal.
func (d *DB) withSettings(ctx context.Context, caller string, begin func(context.Context, func(context.Context) error) error, settings []Setting, fn func(ctx context.Context) error) error {
	applied, err := validateSettings(caller, settings)
	if err != nil {
		return err
	}
	query, args := setConfigStatement(settings)
	return begin(ctx, func(ctx context.Context) error {
		tx, _ := d.TxFrom(ctx)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("pgxdb: applying transaction settings: %w", err)
		}
		tx.settings = applied
		return fn(ctx)
	})
}

// Setting reports the value TransactWithSettings or
// TransactSnapshotWithSettings applied to this transaction under name, from
// memory. It is ("", false) for a transaction begun by Transact or
// TransactSnapshot, or when name was not applied.
func (t *Tx) Setting(name string) (string, bool) {
	value, ok := t.settings[name]
	return value, ok
}

// validateSettings applies Setting's rules and returns them keyed by name.
// Errors name the offending setting but never echo its value.
func validateSettings(caller string, settings []Setting) (map[string]string, error) {
	if len(settings) == 0 {
		return nil, fmt.Errorf("pgxdb: %s needs at least one setting: %w", caller, sdk.ErrInvalidInput)
	}
	applied := make(map[string]string, len(settings))
	for _, s := range settings {
		if !settingName.MatchString(s.Name) {
			return nil, fmt.Errorf("pgxdb: transaction setting name %q is not a lowercase dotted custom parameter: %w", s.Name, sdk.ErrInvalidInput)
		}
		if _, dup := applied[s.Name]; dup {
			return nil, fmt.Errorf("pgxdb: transaction setting %q is named twice: %w", s.Name, sdk.ErrInvalidInput)
		}
		switch {
		case s.Value == "":
			return nil, fmt.Errorf("pgxdb: transaction setting %q has an empty value: %w", s.Name, sdk.ErrInvalidInput)
		case !utf8.ValidString(s.Value):
			return nil, fmt.Errorf("pgxdb: transaction setting %q value is not valid UTF-8: %w", s.Name, sdk.ErrInvalidInput)
		case strings.IndexByte(s.Value, 0) >= 0:
			return nil, fmt.Errorf("pgxdb: transaction setting %q value contains a NUL byte: %w", s.Name, sdk.ErrInvalidInput)
		}
		applied[s.Name] = s.Value
	}
	return applied, nil
}

// setConfigStatement builds one SELECT set_config($1, $2, true), ... with
// every name and value bound.
func setConfigStatement(settings []Setting) (string, []any) {
	calls := make([]string, 0, len(settings))
	args := make([]any, 0, 2*len(settings))
	for i, s := range settings {
		calls = append(calls, "set_config($"+strconv.Itoa(2*i+1)+", $"+strconv.Itoa(2*i+2)+", true)")
		args = append(args, s.Name, s.Value)
	}
	return "SELECT " + strings.Join(calls, ", "), args
}
