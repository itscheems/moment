package cli

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"strings"
)

// ExitCode is the stable process status exposed by the Moment CLI.
type ExitCode int

const (
	// ExitSuccess reports a completed operation or safe idempotent replay.
	ExitSuccess ExitCode = 0
	// ExitInvalidInput reports invalid arguments, flags, or input formats.
	ExitInvalidInput ExitCode = 2
	// ExitNotFound reports a missing resource.
	ExitNotFound ExitCode = 3
	// ExitConflict reports domain validation or conflict failures.
	ExitConflict ExitCode = 4
	// ExitUnauthorized reports authentication or authorization failures.
	ExitUnauthorized ExitCode = 5
	// ExitIO reports filesystem or media I/O failures.
	ExitIO ExitCode = 6
	// ExitIntegrity reports data, archive, or checksum integrity failures.
	ExitIntegrity ExitCode = 7
	// ExitUnavailable reports an unavailable API, network, or dependency.
	ExitUnavailable ExitCode = 8
	// ExitTimeout reports timeout or cancellation.
	ExitTimeout ExitCode = 9
	// ExitInternal reports an unclassified internal failure.
	ExitInternal ExitCode = 10
)

// CommandError is safe to expose through the CLI machine contract.
// Cause is deliberately excluded from serialized and human output.
type CommandError struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	Suggestion string         `json:"suggestion"`
	ExitCode   ExitCode       `json:"-"`
	Cause      error          `json:"-"`
}

func (e *CommandError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewCommandError builds a stable public failure. Callers should branch on Code,
// never Message.
func NewCommandError(code, message, suggestion string, exitCode ExitCode) *CommandError {
	return &CommandError{
		Code:       code,
		Message:    message,
		Suggestion: suggestion,
		ExitCode:   exitCode,
	}
}

// WithDetails returns the error with structured, non-secret diagnostic fields.
func (e *CommandError) WithDetails(details map[string]any) *CommandError {
	e.Details = details
	return e
}

// WithCause retains an internal cause without exposing it in command output.
func (e *CommandError) WithCause(cause error) *CommandError {
	e.Cause = cause
	return e
}

func invalidArgument(message, suggestion string) *CommandError {
	return NewCommandError("INVALID_ARGUMENT", message, suggestion, ExitInvalidInput)
}

func normalizeError(err error, commandStarted bool) *CommandError {
	if err == nil {
		return nil
	}

	var commandError *CommandError
	if errors.As(err, &commandError) {
		if validCommandError(commandError) {
			return commandError
		}
		return internalError(err)
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return NewCommandError(
			"TIMEOUT",
			"the operation exceeded its timeout",
			"increase --timeout or inspect the dependency before retrying",
			ExitTimeout,
		).WithCause(err)
	case errors.Is(err, context.Canceled):
		return NewCommandError(
			"CANCELLED",
			"the operation was cancelled",
			"retry when the caller can keep the operation context active",
			ExitTimeout,
		).WithCause(err)
	}

	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return NewCommandError(
			"IO_ERROR",
			"a filesystem operation failed",
			"check the target path and its permissions, then retry",
			ExitIO,
		).WithCause(err)
	}

	var networkError net.Error
	if errors.As(err, &networkError) {
		return NewCommandError(
			"DEPENDENCY_UNAVAILABLE",
			"a network dependency is unavailable",
			"check --url and the server status, then retry",
			ExitUnavailable,
		).WithCause(err)
	}

	if !commandStarted {
		return invalidArgument(err.Error(), "run 'moment --help' to inspect valid commands and flags")
	}

	return internalError(err)
}

func internalError(cause error) *CommandError {
	return NewCommandError(
		"INTERNAL",
		"an internal error prevented the command from completing",
		"retry once; if it repeats, report the INTERNAL code without including secrets",
		ExitInternal,
	).WithCause(cause)
}

func validCommandError(commandError *CommandError) bool {
	if commandError == nil || strings.TrimSpace(commandError.Message) == "" || strings.TrimSpace(commandError.Suggestion) == "" {
		return false
	}
	if commandError.ExitCode < ExitInvalidInput || commandError.ExitCode > ExitInternal {
		return false
	}
	if commandError.Code == "" || commandError.Code[0] < 'A' || commandError.Code[0] > 'Z' {
		return false
	}
	for _, character := range commandError.Code {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
