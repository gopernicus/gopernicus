package turso

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

func probeCanonicalSchema(ctx context.Context, db *tursodb.DB, audit bool) error {
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	data, err := MigrationsFS.ReadFile(MigrationsDir + "/0001_iam_tuples.sql")
	if err != nil {
		return err
	}
	// Compare the owned CREATE body, retaining SQL literals and grouping. This
	// checks column types/nullability, full identity, collation and every shape
	// constraint together.
	definitions := regexp.MustCompile(`(?s)CREATE TABLE main\.(iam_tuples|iam_audit) (\(.*?\n\));`).FindAllStringSubmatch(string(data), -1)
	if len(definitions) != 2 {
		return incompatibleSchema("iam_tuples", "embedded canonical definitions unavailable")
	}
	for _, def := range definitions {
		table := def[1]
		if table == "iam_audit" {
			if !audit {
				continue
			}
		}
		var actual string
		if err := tx.QueryRow(ctx, `SELECT sql FROM main.sqlite_schema WHERE type='table' AND name=? AND tbl_name=?`, table, table).Scan(&actual); err != nil {
			return err
		}
		start := strings.IndexByte(actual, '(')
		if start < 0 || strings.TrimSpace(actual[start:]) != def[2] {
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
