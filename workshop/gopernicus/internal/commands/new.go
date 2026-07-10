package commands

import (
	"fmt"
	"io"
	"os"
)

// runNew is the second level of the command tree; it routes to a `new`
// subcommand.
func runNew(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "gopernicus new: subcommand required\n\n")
		newUsage(os.Stderr)
		return 1
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "feature":
		return runNewFeature(rest)
	case "-h", "--help", "help":
		newUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gopernicus new: unknown subcommand %q\n\n", sub)
		newUsage(os.Stderr)
		return 1
	}
}

func newUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus new — scaffold a new component

Usage:
  gopernicus new <subcommand>

Subcommands:
  feature   Scaffold a feature module tree
`)
}
