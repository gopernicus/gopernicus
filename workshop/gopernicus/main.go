package main

import (
	"context"
	"fmt"
	"os"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"github.com/gopernicus/gopernicus/workshop/codegen/goversion"
	_ "github.com/gopernicus/gopernicus/workshop/gopernicus/commands"
)

func main() {
	if err := goversion.Check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := cli.Execute(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
