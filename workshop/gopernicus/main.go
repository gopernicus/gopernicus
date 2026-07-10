package main

import (
	"os"

	"github.com/gopernicus/gopernicus/workshop/gopernicus/internal/commands"
)

func main() {
	os.Exit(commands.Run(os.Args[1:]))
}
