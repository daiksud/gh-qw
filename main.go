package main

import (
	"context"
	"os"

	"github.com/daiksud/gh-qw/internal/cmd"
)

var version = "dev"

func main() {
	os.Exit(cmd.Execute(context.Background(), os.Args[1:], cmd.ApplicationDependencies{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}))
}
