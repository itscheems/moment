// Package main composes the Moment executable.
package main

import (
	"context"
	"os"

	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/transport/cli"
)

func main() {
	application := cli.NewMoment(buildinfo.Current())
	exitCode := application.Execute(context.Background(), os.Args[1:], cli.Streams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	})
	os.Exit(int(exitCode))
}
