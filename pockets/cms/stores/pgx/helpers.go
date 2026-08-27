package pgx

import "embed"

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"
