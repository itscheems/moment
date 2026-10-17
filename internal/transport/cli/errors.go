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

// canonicalPublicError maps one stable domain/API code to the same CLI exit
// and remediation regardless of the selected transport. Message and details
// remain the domain-owned values and are never normalized for parity.
func canonicalPublicError(code, message string, details map[string]any, httpStatus int) *CommandError {
	exit := ExitInternal
	suggestion := "retry once; if it repeats, inspect the selected Moment instance"
	switch code {
	case "INVALID_ARGUMENT", "MODE_UNSUPPORTED":
		exit = ExitInvalidInput
		suggestion = "correct the named input or transport mode and retry"
	case "POST_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "run 'moment post list --json' to inspect available posts"
	case "COMMENT_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "run 'moment comment list <post-id> --json' to inspect available comments"
	case "API_TOKEN_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "list the authenticated account's API tokens and select an existing credential"
	case "AUTH_UNAVAILABLE":
		exit = ExitUnavailable
		suggestion = "inspect the account database locally before retrying authentication"
	case "MEDIA_IN_USE":
		exit = ExitConflict
		suggestion = "remove all post, comment, profile and link references before deleting media"
	case "MEDIA_UPLOAD_TIMEOUT":
		exit = ExitTimeout
		suggestion = "retry the upload on a connection that can sustain progress"
	case "MEDIA_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "run 'moment media list --data-dir <path> --json' on the instance host to inspect managed media"
	case "CONTENT_EMPTY", "CONTENT_TOO_LARGE", "EXTERNAL_IMAGE_NOT_ALLOWED":
		exit = ExitConflict
		suggestion = "correct the content and retry"
	case "IDEMPOTENCY_KEY_REUSED":
		exit = ExitConflict
		suggestion = "reuse the original payload or provide a new idempotency key"
	case "COMMENT_DEPTH_EXCEEDED":
		exit = ExitConflict
		suggestion = "reply only to a top-level comment"
	case "COMMENTS_DISABLED":
		exit = ExitConflict
		suggestion = "enable comments in site settings before retrying"
	case "COMMENT_REJECTED", "RATE_LIMITED":
		exit = ExitConflict
		suggestion = "correct the comment input or retry after the reported limit"
	case "MEDIA_TYPE_UNSUPPORTED", "MEDIA_TOO_LARGE", "MEDIA_DIMENSIONS_EXCEEDED":
		exit = ExitConflict
		suggestion = "provide an image within the documented media type and size limits"
	case "MEDIA_CORRUPT":
		exit = ExitIntegrity
		suggestion = "run 'moment media verify --data-dir <path> --json' and restore the affected original"
	case "MEDIA_PREVIEW_FAILED":
		exit = ExitIO
		suggestion = "verify originals, then rerun 'moment media rebuild-previews --data-dir <path> --yes --json'"
	// A device sign-in ends in one of these far more often than it ends in a
	// fault, and each one asks something different of the person waiting.
	case "OAUTH_DEVICE_EXPIRED":
		exit = ExitTimeout
		suggestion = "run the login command again to request a new code"
	case "OAUTH_DEVICE_DENIED":
		exit = ExitUnauthorized
		suggestion = "approve the code at the provider, or run the login command again"
	case "OAUTH_PROVIDER_UNAVAILABLE":
		exit = ExitUnavailable
		suggestion = "check that the provider is configured on the selected Moment server"
	case "UNAUTHORIZED", "CSRF_INVALID", "ACCOUNT_TOKEN_REQUIRED", "ACCOUNT_SUSPENDED", "ACCOUNT_NOT_APPROVED":
		exit = ExitUnauthorized
		suggestion = "use valid credentials belonging to an admitted account"
	case "INSTANCE_IN_USE":
		exit = ExitConflict
		suggestion = "stop the running server or use the supported API operation"
	case "SIGNING_ALREADY_INITIALIZED":
		exit = ExitConflict
		suggestion = "inspect the existing account with 'moment auth status --username <name> --json'"
	case "SIGNING_NOT_INITIALIZED":
		exit = ExitConflict
		suggestion = "run 'moment init --username <name> --data-dir <path> --json' while the server is stopped"
	case "IMPORT_IN_PROGRESS", "IMPORT_STAGING_INVALID", "IMPORT_STAGING_EXPIRED":
		exit = ExitConflict
		suggestion = "inspect 'moment backup status --json' and finish or retry the current recovery operation"
	case "IMPORT_MERGE_PLAN_STALE":
		exit = ExitConflict
		suggestion = "run 'moment backup merge-plan' again, review the new selection, and retry"
	case "IMPORT_OWNER_MISMATCH":
		exit = ExitConflict
		suggestion = "select a portable backup created by the same Moment owner"
	case "MAINTENANCE_MODE":
		exit = ExitUnavailable
		suggestion = "wait for the active maintenance window to finish, then retry"
	case "IMPORT_DISK_SPACE_INSUFFICIENT":
		exit = ExitIO
		suggestion = "free the reported additional bytes on the data filesystem, then retry"
	case "DATA_INTEGRITY_FAILED", "EXPORT_INVALID", "EXPORT_CHECKSUM_MISMATCH", "EXPORT_VERSION_UNSUPPORTED":
		exit = ExitIntegrity
		suggestion = "run diagnostics and restore or recreate the invalid data before retrying"
	case "CANCELLED", "TIMEOUT", "EXPORT_PREPARATION_TIMEOUT":
		exit = ExitTimeout
		suggestion = "retry when the caller can keep the operation context active"
	case "EXPORT_UNAVAILABLE", "DOCTOR_UNAVAILABLE", "NOT_READY", "API_ROUTE_NOT_FOUND", "ROUTE_NOT_FOUND", "METHOD_NOT_ALLOWED", "WEB_ASSETS_UNAVAILABLE":
		exit = ExitUnavailable
		suggestion = "inspect the running Moment server and retry"
	case "REQUEST_BODY_INVALID", "REQUEST_BODY_TOO_LARGE":
		exit = ExitInvalidInput
		suggestion = "correct or reduce the command input and retry"
	case "DOCTOR_INVALID_REPORT":
		exit = ExitInternal
		suggestion = "retry once; if it repeats, report the DOCTOR_INVALID_REPORT code without including secrets"
	case "INTERNAL":
		exit = ExitInternal
		suggestion = "retry once; if it repeats, report the INTERNAL code without including secrets"
	default:
		if httpStatus != 0 {
			exit = ExitUnavailable
			suggestion = "inspect the running Moment server and retry"
			if httpStatus >= 400 && httpStatus < 500 {
				exit = ExitConflict
			}
		}
	}
	if strings.TrimSpace(message) == "" {
		message = "the Moment operation failed"
	}
	return NewCommandError(code, message, suggestion, exit).WithDetails(details)
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
	var outputError *commandOutputError
	if errors.As(err, &outputError) {
		return internalError(outputError)
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
	return validStableErrorCode(commandError.Code)
}

func validStableErrorCode(code string) bool {
	if code == "" || code[0] < 'A' || code[0] > 'Z' {
		return false
	}
	for _, character := range code {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
