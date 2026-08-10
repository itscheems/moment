package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

func enforceSafety(
	streams Streams,
	options Options,
	policy SafetyPolicy,
	state SafetyState,
) *CommandError {
	if !policy.Destructive || state.DryRun || state.Yes {
		return nil
	}
	if policy.DeferredConfirmation {
		if options.NoInput || options.JSON {
			return invalidArgument(
				"confirmation is required but interactive input is disabled",
				"rerun with --yes after reviewing the scope, or use --dry-run when supported",
			)
		}
		return nil
	}
	return promptForConfirmation(streams, options, policy.Confirmation)
}

func promptForConfirmation(streams Streams, options Options, prompt string) *CommandError {

	if options.NoInput || options.JSON {
		return invalidArgument(
			"confirmation is required but interactive input is disabled",
			"rerun with --yes after reviewing the scope, or use --dry-run when supported",
		)
	}

	if prompt == "" {
		prompt = "Continue with this destructive operation?"
	}
	if _, err := fmt.Fprintf(streams.Err, "%s [y/N] ", prompt); err != nil {
		return NewCommandError(
			"IO_ERROR",
			"the confirmation prompt could not be written",
			"check stderr and retry with --yes after reviewing the scope",
			ExitIO,
		).WithCause(err)
	}

	answer, err := bufio.NewReader(streams.In).ReadString('\n')
	if err != nil && err != io.EOF {
		return NewCommandError(
			"IO_ERROR",
			"the confirmation response could not be read",
			"check stdin and retry, or use --no-input with all required flags",
			ExitIO,
		).WithCause(err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return NewCommandError(
			"CONFIRMATION_DECLINED",
			"the destructive operation was not confirmed",
			"review the scope and retry when the operation should proceed",
			ExitConflict,
		)
	}
	return nil
}
