package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/moment/moment/internal/doctor"
	"github.com/spf13/pflag"
)

func registerDoctor(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"doctor"},
		Short:      "Diagnose runtime and data health",
		Long:       "Run a deterministic read-only diagnostic report. Summary mode is fast and safe over direct or account-authenticated API transport; --deep is local/direct only and scans SQLite, media, previews, search, and import state within hard bounds.",
		Inputs:     "A direct --data-dir or account-authenticated --url. Add --deep only with direct mode for expensive topology-sensitive checks.",
		Output:     "Doctor JSON schema v1 with a stable overall status and ordered per-check IDs, severities, bounded evidence, and remediation hints.",
		Errors:     "Authoritative failures preserve the complete report with ok=false and a deterministic non-zero exit; cancellation returns exit 9. API --deep returns MODE_UNSUPPORTED.",
		Safety:     "Read-only. Doctor never repairs, migrates, cleans staging, generates previews, rebuilds search, or reads secret contents.",
		Example:    "moment doctor --deep --data-dir ./moment-data --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.Bool("deep", false, "run bounded local SQLite, media, search, and import integrity checks")
		},
		Run: runDoctor,
	})
}

func runDoctor(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	deep, err := input.Bool("deep")
	if err != nil {
		return Result{}, err
	}
	if invocation.Mode() == ModeAPI && deep {
		return Result{}, NewCommandError(
			"MODE_UNSUPPORTED",
			"api mode does not support 'moment doctor --deep'",
			"remove --deep for the safe API summary, or use --data-dir for a local deep report",
			ExitInvalidInput,
		).WithDetails(map[string]any{"command": "doctor", "mode": ModeAPI, "deep": true})
	}

	mode := doctor.ModeSummary
	if deep {
		mode = doctor.ModeDeep
	}
	var report doctor.Report
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		service, serviceErr := doctor.New(doctor.Options{
			DataDir: target.DataDirectory(),
			Progress: func(progress doctor.Progress) {
				invocation.LogEvent("info", "doctor_progress", map[string]any{
					"check_id": progress.CheckID, "completed": progress.Completed, "total": progress.Total,
				})
			},
		})
		if serviceErr != nil {
			return Result{}, serviceErr
		}
		report, err = service.Run(ctx, mode)
	case ModeAPI:
		var data []byte
		var status int
		data, meta, status, err = authenticatedAPIRawRequest(ctx, invocation, http.MethodGet, "/api/v1/diagnostics", true)
		if err == nil && (status != http.StatusOK || !validDoctorAPIMeta(meta)) {
			err = invalidAPIResponse()
		}
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			decoder.UseNumber()
			if decodeErr := decoder.Decode(&report); decodeErr != nil || decoder.Decode(&struct{}{}) != io.EOF {
				err = invalidAPIResponse()
			}
		}
	default:
		return Result{}, errors.New("unsupported doctor transport")
	}

	if err != nil && invocation.Mode() == ModeAPI && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return Result{}, err
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return Result{}, err
	}
	if validationErr := doctor.ValidateReport(report, mode); validationErr != nil {
		return Result{}, NewCommandError(
			"DEPENDENCY_INVALID_RESPONSE",
			"the doctor report did not match JSON schema v1",
			"upgrade the Moment client or server and retry",
			ExitUnavailable,
		).WithCause(validationErr)
	}
	result := Result{Data: report, Text: doctorText(report), Meta: meta}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			result.Failure = NewCommandError("TIMEOUT", "the doctor report exceeded its timeout", "increase --timeout and retry after active maintenance completes", ExitTimeout).WithCause(err)
			return result, nil
		}
		if errors.Is(err, context.Canceled) {
			result.Failure = NewCommandError("CANCELLED", "the doctor report was cancelled", "retry when a complete report is required", ExitTimeout).WithCause(err)
			return result, nil
		}
		return Result{}, err
	}
	result.Failure = doctorCommandFailure(report.Failure())
	return result, nil
}

func validDoctorAPIMeta(meta map[string]any) bool {
	if len(meta) != 1 {
		return false
	}
	requestID, ok := meta["request_id"].(string)
	return ok && strings.TrimSpace(requestID) != ""
}

func doctorCommandFailure(kind doctor.FailureKind) *CommandError {
	switch kind {
	case doctor.FailureNone:
		return nil
	case doctor.FailureUnavailable:
		return NewCommandError("DEPENDENCY_UNAVAILABLE", "one or more diagnostic dependencies are unavailable", "inspect the failed checks and retry after active maintenance stops", ExitUnavailable)
	case doctor.FailureIO:
		return NewCommandError("IO_ERROR", "one or more filesystem diagnostic checks failed", "inspect the failed checks and repair access without changing healthy data", ExitIO)
	case doctor.FailureIntegrity:
		return NewCommandError("DATA_INTEGRITY_FAILED", "one or more authoritative integrity checks failed", "inspect each failed check and restore from a verified backup when required", ExitIntegrity)
	case doctor.FailureCancelled:
		return NewCommandError("CANCELLED", "the doctor report was cancelled", "retry when a complete report is required", ExitTimeout)
	default:
		return NewCommandError("INTERNAL", "one or more diagnostic checks failed internally", "inspect stderr and retry with a supported Moment binary", ExitInternal)
	}
}

func doctorText(report doctor.Report) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Moment doctor: %s (%s)\n", strings.ToUpper(string(report.Status)), report.Mode)
	for _, check := range report.Checks {
		fmt.Fprintf(&output, "[%s] %s: %s\n", strings.ToUpper(string(check.Status)), check.ID, check.Summary)
		if check.Status == doctor.StatusFail || check.Status == doctor.StatusWarning {
			fmt.Fprintf(&output, "  Remediation: %s\n", check.Remediation)
		}
	}
	return strings.TrimSuffix(output.String(), "\n")
}
