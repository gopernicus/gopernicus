// Package commands is the gopernicus CLI dispatcher: a two-level
// command/subcommand parser built on the standard library's flag package. Each
// command owns its own file; unknown commands and subcommands write usage to
// stderr and exit non-zero.
package commands

import (
	"fmt"
	"io"
	"os"
)

const (
	// modulePath is this CLI's own module path — printed by `version` as the
	// smallest end-to-end proof that dispatch works.
	modulePath = "github.com/gopernicus/gopernicus/workshop/gopernicus"

	// version is a placeholder until the module carries a real tag.
	version = "0.0.0-dev"
)

// Run dispatches argv (without the program name) to a top-level command and
// returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 1
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		return runVersion(rest)
	case "init":
		return runInit(rest)
	case "new":
		return runNew(rest)
	case "db":
		return runDB(rest)
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gopernicus: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return 1
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `gopernicus — the gopernicus scaffolding CLI

Usage:
  gopernicus <command> [flags]

Commands:
  init      Scaffold a new host application
  new       Scaffold a new feature
  db        Migrate, check status, or create migrations
  version   Print the CLI version and module path

Run 'gopernicus <command> -h' for command-specific help.
`)
}
