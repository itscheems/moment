// Package main composes the Moment executable.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/moment/moment/internal/buildinfo"
)

const usage = `Moment — a portable personal timeline

Usage:
  moment version [--json]

Run "moment version --json" to inspect this build.`

func main() {
	if len(os.Args) == 1 {
		writeLine(os.Stdout, usage)
		return
	}

	switch os.Args[1] {
	case "version":
		if len(os.Args) == 3 && os.Args[2] == "--json" {
			if err := json.NewEncoder(os.Stdout).Encode(buildinfo.Current()); err != nil {
				writeFormat(os.Stderr, "encode version: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if len(os.Args) == 2 {
			info := buildinfo.Current()
			writeFormat(os.Stdout, "moment %s (%s, %s)\n", info.Version, info.Commit, info.BuildDate)
			return
		}
		writeLine(os.Stderr, "usage: moment version [--json]")
		os.Exit(2)
	case "help", "--help", "-h":
		writeLine(os.Stdout, usage)
	default:
		writeFormat(os.Stderr, "unknown command %q\n", os.Args[1])
		writeLine(os.Stderr, "run 'moment --help' for usage")
		os.Exit(2)
	}
}

func writeLine(w io.Writer, value string) {
	if _, err := fmt.Fprintln(w, value); err != nil {
		os.Exit(1)
	}
}

func writeFormat(w io.Writer, format string, values ...any) {
	if _, err := fmt.Fprintf(w, format, values...); err != nil {
		os.Exit(1)
	}
}
