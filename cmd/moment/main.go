// Package main composes the Moment executable.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/transport/cli"
)

func main() {
	ignoreBrokenPipeSignal()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application := cli.NewMoment(buildinfo.Current())
	exitCode := application.Execute(ctx, os.Args[1:], cli.Streams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	})
	os.Exit(int(exitCode))
}
