package commands

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"github.com/gopernicus/gopernicus/workshop/codegen/initialize"
)

func init() {
	cli.RegisterCommand(&cli.Command{
		Name:  "init",
		Short: "Bootstrap a new gopernicus project (flags only)",
		Long: `Bootstrap a new gopernicus project in a new directory.

Scaffolds a project directory with go.mod, gopernicus.yml, and a minimal
directory layout ready for 'gopernicus generate'. This in-framework tool is
flags-only: every choice is a flag, and omitted flags use the defaults (all
features, default infrastructure). For the interactive picker experience,
use the gopernicus CLI: go run github.com/gopernicus/gopernicus-cli@latest init

Bootstrap a project with no second install:
  go run github.com/gopernicus/gopernicus/workshop/gopernicus@latest init myapp --no-interactive

Examples:
  gopernicus init myapp
  gopernicus init myapp --module github.com/acme/myapp
  gopernicus init myapp --no-interactive --features=authentication,authorization
  gopernicus init myapp --no-interactive --features=none
  gopernicus init myapp --framework-version v0.1.0`,
		Usage: "gopernicus init <project-name> [--module <path>] [--framework-version <version>] [--no-interactive] [--features <list>]",
		Run:   runInit,
	})
}

func runInit(_ context.Context, args []string) error {
	opts, err := initialize.ParseArgs(args)
	if err != nil {
		return err
	}

	// This tool has no TUI: init is always non-interactive here. When the
	// user didn't ask for that explicitly, point at the CLI's picker flow.
	if !opts.NoInteractive {
		fmt.Println("note: using defaults — the interactive picker lives in the gopernicus CLI (go run github.com/gopernicus/gopernicus-cli@latest init)")
		opts.NoInteractive = true
	}

	if err := initialize.ResolveDefaults(&opts); err != nil {
		return err
	}

	return initialize.Run(opts)
}
