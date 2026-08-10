package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/moment/moment/internal/observability"
)

var logPriorities = map[string]int{
	"error": 0,
	"warn":  1,
	"info":  2,
	"debug": 3,
}

type logger struct {
	level  int
	format string
	writer io.Writer
	events *observability.Logger
}

func newLogger(level, format string, writer io.Writer) (logger, error) {
	priority, ok := logPriorities[level]
	if !ok {
		return logger{}, invalidArgument(
			fmt.Sprintf("invalid log level %q", level),
			"use --log-level error, warn, info, or debug",
		)
	}
	events, err := observability.New(observability.Config{Level: level, Format: format, Writer: writer})
	if err != nil {
		return logger{}, invalidArgument(
			fmt.Sprintf("invalid log format %q", format),
			"use --log-format text or json",
		)
	}
	return logger{level: priority, format: format, writer: writer, events: events}, nil
}

func (output logger) log(level, message string) {
	priority, ok := logPriorities[level]
	if !ok || priority > output.level || output.writer == nil {
		return
	}
	message = strings.TrimRight(message, "\r\n")
	if output.format == string(observability.FormatJSON) {
		output.events.Event(level, "cli_diagnostic", observability.Fields{"message": message})
		return
	}
	_, _ = fmt.Fprintf(output.writer, "%s: %s\n", level, message)
}

func (output logger) event(level, event string, fields map[string]any) {
	if output.events == nil {
		return
	}
	output.events.Event(level, event, observability.Fields(fields))
}
