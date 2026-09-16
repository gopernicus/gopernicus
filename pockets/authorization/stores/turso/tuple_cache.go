package turso

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// TupleCacheMigrationsFS holds optional raw tuple capture. Apply after the
// primary authorization migrations in the same database/schema.
//
//go:embed tuple_cache_migrations/*.sql
var TupleCacheMigrationsFS embed.FS

const TupleCacheMigrationsDir = "tuple_cache_migrations"
const TupleCacheMigrationSource = "authorization-cache-v2"

func ExportTupleCacheMigrations(dst string) error {
	return tursodb.ExportMigrations(TupleCacheMigrationsFS, TupleCacheMigrationsDir, dst)
}

// WithTupleCache probes and exposes the authoritative tuple source. Installing
// the optional migration captures every ordinary SQL tuple mutation regardless
// of whether its writer enables this option. Construction starts no worker.
func WithTupleCache() Option { return func(c *config) { c.tupleCache = true } }

func prepareTupleSource(ctx context.Context, db *tursodb.DB, cfg *config) (*tupleSource, error) {
	if _, ambient := tursodb.TxFromContext(ctx); ambient {
		return nil, fmt.Errorf("authorization tuple cache: ambient construction: %w", sdk.ErrInvalidInput)
	}
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	data, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql")
	if err != nil {
		return nil, err
	}
	// Preserve literals when checking owned SQL; SQLite removes only the final
	// semicolon from these canonical CREATE definitions in sqlite_schema.
	definitions := regexp.MustCompile(`(?s)CREATE TABLE main\.(iam_tuple_\w+) \(.*?\n\);|CREATE TRIGGER main\.(\w+).*?\nEND;`).FindAllStringSubmatch(string(data), -1)
	if len(definitions) != 5 {
		return nil, tuplecache.ErrUnavailable
	}
	for _, def := range definitions {
		name, kind, table := def[1], "table", def[1]
		if name == "" {
			name, kind, table = def[2], "trigger", "iam_tuples"
		}
		var actual string
		if err := tx.QueryRow(ctx, "SELECT sql FROM main.sqlite_schema WHERE name=? AND type=? AND tbl_name=?", name, kind, table).Scan(&actual); err != nil {
			return nil, fmt.Errorf("authorization TupleCache definition %s missing: %w", name, err)
		}
		if strings.TrimSpace(actual) != strings.TrimSuffix(strings.Replace(def[0], "main.", "", 1), ";") {
			return nil, fmt.Errorf("authorization TupleCache definition %s incompatible: %w", name, tuplecache.ErrUnavailable)
		}
	}
	source := &tupleSource{db: db}
	binding, _, err := source.readHead(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	cfg.tupleBinding = fmt.Sprintf("%x", sha256.Sum256([]byte("turso/main/authorization-tuples/v2/"+binding)))
	source.cfg, source.binding = *cfg, binding
	return source, nil
}

func (s *tupleSource) readHead(ctx context.Context, q tursodb.Querier) (binding, receipt string, err error) {
	rows, err := q.Query(ctx, "SELECT slot, protocol, binding, receipt, typeof(slot), typeof(protocol), typeof(binding), typeof(receipt) FROM main.iam_tuple_cache")
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var slot, protocol int64
		var slotType, protocolType, bindingType, receiptType string
		if err := rows.Scan(&slot, &protocol, &binding, &receipt, &slotType, &protocolType, &bindingType, &receiptType); err != nil {
			return "", "", fmt.Errorf("%w: %v", tuplecache.ErrUnavailable, err)
		}
		count++
		if count != 1 || slot != 1 || protocol != 2 || slotType != "integer" || protocolType != "integer" || bindingType != "text" || receiptType != "text" {
			return "", "", tuplecache.ErrUnavailable
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if count != 1 || len(binding) != 32 || strings.Trim(binding, "0123456789abcdef") != "" {
		return "", "", tuplecache.ErrUnavailable
	}
	if s.binding != "" && binding != s.binding {
		return "", "", tuplecache.ErrBinding
	}
	return binding, receipt, nil
}

func (s *tupleSource) Snapshot(ctx context.Context, mirroredReceipt string) (tuplecache.Snapshot, error) {
	if !s.CacheableContext(ctx) {
		return tuplecache.Snapshot{}, fmt.Errorf("authorization tuple cache: ambient snapshot: %w", sdk.ErrInvalidInput)
	}
	tx, err := s.db.BeginRead(ctx)
	if err != nil {
		return tuplecache.Snapshot{}, err
	}
	defer tx.Rollback()
	_, receipt, err := s.readHead(ctx, tx)
	if err != nil {
		return tuplecache.Snapshot{}, err
	}
	snapshot := tuplecache.Snapshot{Receipt: receipt, Full: mirroredReceipt == "" || mirroredReceipt != receipt}
	snapshot.Changes, err = readTupleChanges(ctx, tx, snapshot.Full)
	if err != nil {
		return tuplecache.Snapshot{}, err
	}
	if snapshot.Full {
		snapshot.Tuples, err = readAllTuples(ctx, tx)
		if err != nil {
			return tuplecache.Snapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return tuplecache.Snapshot{}, err
	}
	return snapshot, nil
}

func readTupleChanges(ctx context.Context, q tursodb.Querier, full bool) ([]tuplecache.Change, error) {
	columns := "id, before_tuple, after_tuple"
	if full {
		// Rebuilds replace pending changes with current facts; retain only the
		// exact event identities needed to acknowledge this source snapshot.
		columns = "id"
	}
	rows, err := q.Query(ctx, "SELECT "+columns+" FROM main.iam_tuple_outbox ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []tuplecache.Change
	for rows.Next() {
		var id int64
		if full {
			if err := rows.Scan(&id); err != nil {
				return nil, tursodb.MapError(err)
			}
			if id <= 0 {
				return nil, tuplecache.ErrUnavailable
			}
			changes = append(changes, tuplecache.Change{ID: strconv.FormatInt(id, 10)})
			continue
		}
		var before, after sql.NullString
		if err := rows.Scan(&id, &before, &after); err != nil {
			return nil, tursodb.MapError(err)
		}
		if id <= 0 {
			return nil, tuplecache.ErrUnavailable
		}
		old, err := decodeTuple(before)
		if err != nil {
			return nil, err
		}
		next, err := decodeTuple(after)
		if err != nil {
			return nil, err
		}
		if old == nil && next == nil {
			return nil, tuplecache.ErrUnavailable
		}
		changes = append(changes, tuplecache.Change{ID: strconv.FormatInt(id, 10), Before: old, After: next})
	}
	return changes, tursodb.MapError(rows.Err())
}

func decodeTuple(value sql.NullString) (*tuples.Tuple, error) {
	if !value.Valid {
		return nil, nil
	}
	// encoding/json replaces malformed UTF-8 with U+FFFD. Never let corrupt
	// source bytes become a different, valid principal or resource identity.
	if !utf8.ValidString(value.String) {
		return nil, tuplecache.ErrUnavailable
	}
	var values []any
	if err := json.Unmarshal([]byte(value.String), &values); err != nil || len(values) != 7 {
		return nil, tuplecache.ErrUnavailable
	}
	var fields [6]string
	kind, ok := values[0].(float64)
	if !ok || (kind != 1 && kind != 2) {
		return nil, tuplecache.ErrUnavailable
	}
	for i, value := range values[1:] {
		field, ok := value.(string)
		if !ok {
			return nil, tuplecache.ErrUnavailable
		}
		fields[i] = field
	}
	tuple := &tuples.Tuple{Scope: tuples.Scope{Kind: tuples.ScopeKind(kind), Type: fields[0], ID: fields[1]}, Relation: fields[2], Subject: tuples.SubjectRef{Type: fields[3], ID: fields[4], Relation: fields[5]}}
	if err := tuple.Validate(); err != nil {
		return nil, fmt.Errorf("tuple cache payload: %w: %v", tuplecache.ErrUnavailable, err)
	}
	return tuple, nil
}

func readAllTuples(ctx context.Context, q tursodb.Querier) ([]tuples.Tuple, error) {
	rows, err := q.Query(ctx, `SELECT scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation FROM main.iam_tuples
ORDER BY scope_kind,resource_type, resource_id, relation, subject_type, subject_id, subject_relation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []tuples.Tuple
	for rows.Next() {
		var tuple tuples.Tuple
		if err := rows.Scan(&tuple.Scope.Kind, &tuple.Scope.Type, &tuple.Scope.ID, &tuple.Relation, &tuple.Subject.Type, &tuple.Subject.ID, &tuple.Subject.Relation); err != nil {
			return nil, tursodb.MapError(err)
		}
		if err := tuple.Validate(); err != nil {
			return nil, fmt.Errorf("tuple cache current fact: %w: %v", tuplecache.ErrUnavailable, err)
		}
		facts = append(facts, tuple)
	}
	return facts, tursodb.MapError(rows.Err())
}

func (s *tupleSource) Acknowledge(ctx context.Context, expectedReceipt, receipt string, ids []string) error {
	if !s.CacheableContext(ctx) || receipt == "" {
		return fmt.Errorf("authorization tuple cache: ambient acknowledgement or empty receipt: %w", sdk.ErrInvalidInput)
	}
	numbers := make([]int64, len(ids))
	for i, id := range ids {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
			return fmt.Errorf("authorization tuple cache: invalid outbox identity: %w", sdk.ErrInvalidInput)
		}
		numbers[i] = n
	}
	payload, err := json.Marshal(numbers)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		_, current, err := s.readHead(ctx, tx)
		if err != nil {
			return err
		}
		if current != expectedReceipt {
			return tuplecache.ErrConflict
		}
		if _, err := tx.Exec(ctx, "UPDATE main.iam_tuple_cache SET receipt = ? WHERE slot = 1 AND binding = ? AND receipt = ?", receipt, s.binding, expectedReceipt); err != nil {
			return err
		}
		// Only captured IDs are disposed. A later commit remains pending even
		// if it happens before this acknowledgement acquires the write lock.
		_, err = tx.Exec(ctx, "DELETE FROM main.iam_tuple_outbox WHERE id IN (SELECT value FROM json_each(?))", string(payload))
		return err
	})
}

var _ tuplecache.Source = (*tupleSource)(nil)
