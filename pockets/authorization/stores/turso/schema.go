package turso

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

func probeCanonicalSchema(ctx context.Context, db *tursodb.DB, audit, operations bool) error {
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/0001_iam_tuples.sql")
	if err != nil {
		return err
	}
	// 0004 rebuilds iam_operations under a temporary name; its CREATE body is the
	// canonical one SQLite keeps after the rename.
	operationsData, err := MigrationsFS.ReadFile(MigrationsDir + "/0004_iam_operations_fenced.sql")
	if err != nil {
		return err
	}
	data = append(data, operationsData...)
	// 0005 rebuilds iam_audit and iam_operations the same way; until it is
	// required, either the earlier body or the 0005 body is canonical.
	integrityData, err := MigrationsFS.ReadFile(MigrationsDir + "/0005_iam_audit_integrity.sql")
	if err != nil {
		return err
	}
	data = append(data, integrityData...)
	// Compare the owned CREATE body, retaining SQL literals and grouping. This
	// checks column types/nullability, full identity, collation and every shape
	// constraint together.
	definitions := regexp.MustCompile(`(?s)CREATE TABLE main\.(iam_tuples|iam_audit|iam_audit_next|iam_operations_next) (\(.*?\n\));`).FindAllStringSubmatch(string(data), -1)
	if len(definitions) != 5 {
		return incompatibleSchema("iam_tuples", "embedded canonical definitions unavailable")
	}
	accepted := map[string][]string{}
	for _, def := range definitions {
		table := strings.TrimSuffix(def[1], "_next")
		accepted[table] = append(accepted[table], def[2])
	}
	for _, table := range []string{"iam_tuples", "iam_audit", "iam_operations"} {
		if table == "iam_audit" {
			if !audit {
				continue
			}
		}
		if table == "iam_operations" && !operations {
			continue
		}
		var actual string
		if err := tx.QueryRow(ctx, `SELECT sql FROM main.sqlite_schema WHERE type='table' AND name=? AND tbl_name=?`, table, table).Scan(&actual); err != nil {
			return err
		}
		start := strings.IndexByte(actual, '(')
		if start < 0 || !slices.Contains(accepted[table], strings.TrimSpace(actual[start:])) {
			return incompatibleSchema(table, "column, primary key or shape definitions differ from the canonical migration")
		}
		var extraUnique int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pragma_index_list(?, 'main') WHERE "unique"=1 AND origin<>'pk'`, table).Scan(&extraUnique); err != nil {
			return err
		}
		if extraUnique != 0 {
			return incompatibleSchema(table, "additional uniqueness restricts canonical facts")
		}
	}
	return tx.Commit()
}

func incompatibleSchema(table, detail string) error {
	return fmt.Errorf("authorization turso store: %s incompatible canonical schema (%s) — apply the %q migration source before boot: %w", table, detail, "authorization", sdk.ErrInvalidInput)
}
