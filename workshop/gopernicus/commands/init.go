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
		Short: "Bootstrap a new gopernicus project",
		Long: `Bootstrap a new gopernicus project in a new directory.

Scaffolds a project directory with go.mod, gopernicus.yml, and a minimal
directory layout ready for 'gopernicus generate'. Every choice is a flag;
omitted flags use the defaults (all features, default infrastructure).

Bootstrap a project with no install:
  go run github.com/gopernicus/gopernicus/workshop/gopernicus@latest init myapp

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

	// init is flag-driven; omitted flags mean defaults.
	if !opts.NoInteractive {
		fmt.Println("note: using defaults — pass --features/--module to customize")
		opts.NoInteractive = true
	}

	if err := initialize.ResolveDefaults(&opts); err != nil {
		return err
	}

	return initialize.Run(opts)
}
