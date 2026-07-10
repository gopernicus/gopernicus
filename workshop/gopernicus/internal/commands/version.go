package commands

import (
	"flag"
	"fmt"
	"os"
)

func runVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "Usage: gopernicus version") }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	fmt.Printf("gopernicus %s\n", version)
	fmt.Println(modulePath)
	return 0
}
