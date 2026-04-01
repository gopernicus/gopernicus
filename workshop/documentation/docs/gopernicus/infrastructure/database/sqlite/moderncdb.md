---
sidebar_position: 1
title: moderncdb
---

# sqlite/moderncdb

SQLite via [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — pure Go, no CGO. Returns `*moderncdb.DB` which wraps `*sql.DB` with integrated error handling and optional tracing.

```go
// File-based — WAL mode enabled by default
db, err := moderncdb.NewFile("./data/app.db")

// In-memory — useful in tests
db, err := moderncdb.NewInMemory()

// Full config
db, err := moderncdb.New(moderncdb.Options{
    Path:         "./data/app.db",
    WALMode:      true,
    ForeignKeys:  true,
    BusyTimeout:  5000,
    MaxOpenConns: 1, // SQLite works best with a single writer
})
```

Access the underlying `*sql.DB` for advanced operations:

```go
sqlDB := db.Underlying()
```

Generated SQLite stores are written against `database/sql` — distinct from pgx stores, which use pgx-native types.

## Error Handling

Sentinels: `ErrNotFound`, `ErrDuplicateEntry`, `ErrConstraintFailed`.
