package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moment/moment/internal/devtools"
	"github.com/moment/moment/internal/domain"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerDev(application *Application) {
	commonFlags := func(flags *pflag.FlagSet) {
		flags.Int64("seed", 1, "deterministic signed 64-bit fixture seed")
	}
	policy := SafetyPolicy{
		Destructive: true, SupportsDryRun: true,
		Confirmation: "Replace the exact marked development instance selected by --data-dir?",
	}
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"dev", "reset"}, Short: "Reset one marked development instance",
		Long:       "Atomically replace one exact, explicitly marked development instance with an empty generation. Production, unmarked, implicit, root, home, symlinked, mounted, or actively served targets are refused.",
		Inputs:     "Requires explicit --data-dir. Use --dry-run to validate the marker, path identities, mount boundaries, and OS locks without writing.",
		Output:     "The stable recipe plan, apply status, and doctor handoff. JSON is one envelope; diagnostics remain on stderr.",
		Errors:     "Unsafe input exits 2, active service exits 4, I/O exits 6, integrity exits 7, cancellation exits 9.",
		Safety:     "Confirmation occurs before the handler. --yes skips only confirmation; every safety guard and lock is still enforced.",
		Example:    "moment dev reset --data-dir ./moment-dev --dry-run --json\nmoment dev reset --data-dir ./moment-dev --yes --no-input --json",
		Capability: CapabilityDirectOnly, Policy: policy, Args: cobra.NoArgs,
		Run: func(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
			return runDev(ctx, invocation, devtools.OperationReset, "", 0)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"dev", "seed"}, Short: "Build the deterministic mixed development fixture",
		Long:       "Atomically seed one marked development instance, or initialize development metadata only when --data-dir names an existing canonical and strictly empty directory.",
		Inputs:     "Requires explicit --data-dir. --seed controls logical IDs, timestamps, content, comments, and media. The default is 1.",
		Output:     "The recipe version, exact counts, doctor status, and a cryptographically random owner token exactly once for a fresh instance.",
		Errors:     "Unknown or unsafe input exits 2, active service exits 4, I/O exits 6, integrity exits 7, cancellation exits 9.",
		Safety:     "Strictly empty bootstrap and marked reset both require confirmation. --dry-run is fully read-only.",
		Example:    "mkdir ./moment-dev && moment dev seed --data-dir ./moment-dev --seed 42 --yes --no-input --json",
		Capability: CapabilityDirectOnly, Policy: policy, Args: cobra.NoArgs, Flags: commonFlags,
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			seed, err := input.Int64("seed")
			if err != nil {
				return Result{}, err
			}
			return runDev(ctx, invocation, devtools.OperationSeed, "", seed)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"dev", "scenario"}, ArgsUsage: "<name>", Short: "Build one named acceptance scenario",
		Long:       "Atomically build one frozen scenario by name. Catalog names, recipe versions, exact counts, healthy doctor postconditions, and the corruption finding ID are shared by help and JSON output. Scale scenarios are long-running and stop on parent cancellation.",
		Inputs:     "One scenario name and explicit --data-dir. Names: " + strings.Join(devtools.ScenarioNames(), ", ") + ".",
		Output:     "The selected scenario plan, exact counts, doctor handoff, and archive roundtrip result when applicable.",
		Errors:     "Unknown names exit 2 and list the canonical catalog. Active service exits 4; I/O 6; integrity 7; parent cancellation 9. Explicit --timeout is not accepted for this long-running command.",
		Safety:     "A strictly empty directory may bootstrap development; any existing unmarked content or production marker is refused.",
		Example:    "mkdir ./moment-dev && moment dev scenario archive-roundtrip --data-dir ./moment-dev --seed 42 --yes --no-input --json",
		Capability: CapabilityDirectOnly, Policy: policy, Args: cobra.ExactArgs(1), Flags: commonFlags, LongRunning: true,
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			seed, err := input.Int64("seed")
			if err != nil {
				return Result{}, err
			}
			return runDev(ctx, invocation, devtools.OperationScenario, input.Args()[0], seed)
		},
	})
}

func runDev(ctx context.Context, invocation Invocation, operation devtools.Operation, scenario string, seed int64) (Result, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	result, err := devtools.Execute(ctx, devtools.Request{
		DataDir: target.DataDirectory(), Operation: operation, Scenario: scenario, Seed: seed,
		DryRun: invocation.Safety().DryRun,
		Progress: func(progress devtools.Progress) {
			invocation.LogEvent("info", "dev_fixture_progress", map[string]any{
				"kind": progress.Kind, "completed": progress.Completed, "total": progress.Total,
			})
		},
	})
	if err != nil {
		return Result{}, devCLIError(err)
	}
	text := fmt.Sprintf("Development %s: scenario=%s entries=%d comments=%d media=%d", result.Plan.Operation, result.Plan.Scenario, result.Plan.Counts.Entries, result.Plan.Counts.Comments, result.Plan.Counts.Media)
	if result.Plan.DryRun {
		text = "Dry run: " + text
	}
	if result.OwnerToken != "" {
		text += "\nOwner token (shown once): " + result.OwnerToken
	}
	return Result{Data: result, Text: text}, nil
}

func devCLIError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		exit := ExitIntegrity
		suggestion := "inspect the exact development root with 'moment doctor --deep --json' before retrying"
		switch domainErr.Code {
		case domain.CodeInvalidArgument:
			exit = ExitInvalidInput
			suggestion = "use an explicit canonical marked development root, or a strictly empty directory for seed/scenario bootstrap"
		case domain.CodeInstanceInUse, domain.CodeImportInProgress:
			exit = ExitConflict
			suggestion = "stop the serving or importing process and retry; --yes cannot bypass OS locks"
		case domain.CodeDataIntegrityFailed:
			exit = ExitIntegrity
		}
		return NewCommandError(string(domainErr.Code), domainErr.Message, suggestion, exit).WithDetails(domainErr.Details).WithCause(err)
	}
	return err
}
