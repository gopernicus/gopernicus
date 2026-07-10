package commands

import (
	"flag"
	"os"
)

// runInit is a stub until W2 emits the host scaffold.
func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	return notImplemented("init")
}
