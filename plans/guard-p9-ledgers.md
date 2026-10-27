# Guard P9 reads every host ledger (workshop/gopernicus v0.9.1)

**Status:** RATIFIED 2026-10-26 (Josh, from the gps-three-sixty M0 amendment A1).
**Release:** `workshop/gopernicus/v0.9.1`, PATCH.

## Why

A host may keep more than one ledger under `workshop/migrations/`, one per database. The
consumer is gps-three-sixty, which keeps authentication and authorization in their own
database (`threesixty_iam`, schema `iam`), as gps-360-go did. `gopernicus db create --db
<ledger>` already scaffolds into any ledger. P9, though, reads only `primary`
(`posture_rules.go`, `defaultLedger`). A host that moves the authentication and authorization
store copies into `workshop/migrations/iam/` fails P9 even though every copy is byte-identical.

## Change

1. P9 reads every ledger: each non-`_`/non-`.` directory under `workshop/migrations/`. With no
   ledger directory at all it reads `primary` as before. Its messages don't change, so the
   absence of a ledger is still visible.
2. **One store, one ledger.** A store's home ledger is the one holding byte-identical copies of
   its files.
   - Copies in more than one ledger is one finding per store, naming the ledgers: a store's
     migrations apply to one database.
   - A missing or edited copy is judged against the home ledger. With no home, it's judged
     against every ledger, and the message names `workshop/migrations/*`.
   - A suffix match in a ledger without byte-identical copies does not make that ledger home
     (hosts may have their own `users.sql`).
3. The status-line detail names each store's ledger **only when the host has more than one
   ledger**, e.g. `(2 stores; ledgers: authentication→iam, jobs→primary)`. A single-`primary`
   host prints exactly what v0.9.0 printed.
4. Declining `*_immutable.sql` is unchanged: an omitted trigger file in the home ledger is
   declined and named.

## Also in this release (owner ruling: yes)

- The `init` and `new pocket` templates move sdk v0.18.0 / v0.15.0 → **v0.20.0**. pgxdb v0.14.0
  and turso v0.9.0 are already the latest.
- The emitted README runs `workshop/gopernicus@v0.9.1 guard --posture .`.

## Tests

- Two ledgers, each store complete in one: clean, with the ledgers named on the status line.
- A store split across two ledgers: finding.
- A copy missing from every ledger: finding.
- An edited copy in the home ledger: finding.
- A declined immutable file in a non-primary ledger: clean, with a detail.
- Every existing P9 test, unchanged: the single-`primary` layout is byte-for-byte v0.9.0's
  output.
- Scaffold tests at the new pins.

## Docs

- `examples/README.md` §3b, "P9 in full": the ledger is any directory under
  `workshop/migrations/`, and a store lives in one.
- A RELEASING.md entry when tagged.

## Out of scope

The `db status` file-only fallback keeps `--db`. The P9 sentence keeps saying "the host
ledger", which now reads as the store's ledger.
