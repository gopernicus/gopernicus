package commands

import (
	"fmt"
	"io"
	"os"
)

// runDB is the second level of the command tree; it routes to a `db`
// subcommand. The subcommands are stubs until W4 wires the host migration seam.
func runDB(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "gopernicus db: subcommand required\n\n")
		dbUsage(os.Stderr)
		return 1
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "migrate":
		return runDBMigrate(rest)
	case "status":
		return runDBStatus(rest)
	case "create":
		return runDBCreate(rest)
	case "-h", "--help", "help":
		dbUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gopernicus db: unknown subcommand %q\n\n", sub)
		dbUsage(os.Stderr)
		return 1
	}
}

func runDBMigrate(_ []string) int {
	return notImplemented("db migrate")
}

func runDBStatus(_ []string) int {
	return notImplemented("db status")
}

func runDBCreate(_ []string) int {
	return notImplemented("db create")
}

func dbUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus db — host migration utilities

Usage:
  gopernicus db <subcommand>

Subcommands:
  migrate   Apply pending migrations from the host ledger
  status    Show applied and pending migrations
  create    Scaffold a new migration file
`)
}
