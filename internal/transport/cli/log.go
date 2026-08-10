package cli

import (
	"fmt"
	"io"
	"strings"
)

var logPriorities = map[string]int{
	"error": 0,
	"warn":  1,
	"info":  2,
	"debug": 3,
}

type logger struct {
	level  int
	writer io.Writer
}

func newLogger(level string, writer io.Writer) (logger, error) {
	priority, ok := logPriorities[level]
	if !ok {
		return logger{}, invalidArgument(
			fmt.Sprintf("invalid log level %q", level),
			"use --log-level error, warn, info, or debug",
		)
	}
	return logger{level: priority, writer: writer}, nil
}

func (output logger) log(level, message string) {
	priority, ok := logPriorities[level]
	if !ok || priority > output.level || output.writer == nil {
		return
	}
	message = strings.TrimRight(message, "\r\n")
	_, _ = fmt.Fprintf(output.writer, "%s: %s\n", level, message)
}
