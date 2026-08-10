package doctor

import "fmt"

type reportBuilder struct {
	report   Report
	index    map[string]int
	progress func(Progress)
}

func newReport(mode Mode, progress func(Progress)) *reportBuilder {
	checks := make([]Check, len(registry))
	index := make(map[string]int, len(registry))
	for position, id := range registry {
		index[id] = position
		checks[position] = Check{
			ID: id, Status: StatusSkipped, Severity: SeverityInfo,
			Summary: "check was not reached", Evidence: map[string]any{"reason": "not_reached"},
			Remediation: "resolve earlier failed checks and run doctor again",
		}
	}
	return &reportBuilder{report: Report{SchemaVersion: SchemaVersion, Mode: mode, Checks: checks}, index: index, progress: progress}
}

func (builder *reportBuilder) set(check Check) {
	position, ok := builder.index[check.ID]
	if !ok {
		panic(fmt.Sprintf("unregistered doctor check %q", check.ID))
	}
	if check.Evidence == nil {
		check.Evidence = map[string]any{}
	}
	builder.report.Checks[position] = check
	if builder.progress != nil {
		completed := 0
		for _, result := range builder.report.Checks {
			if result.Status != StatusSkipped || result.Evidence["reason"] != "not_reached" {
				completed++
			}
		}
		builder.progress(Progress{Completed: completed, Total: len(registry), CheckID: check.ID})
	}
}

func pass(id, summary, remediation string, evidence map[string]any) Check {
	return Check{ID: id, Status: StatusPass, Severity: SeverityInfo, Summary: summary, Evidence: evidence, Remediation: remediation}
}

func warning(id, summary, remediation string, evidence map[string]any) Check {
	return Check{ID: id, Status: StatusWarning, Severity: SeverityWarning, Summary: summary, Evidence: evidence, Remediation: remediation}
}

func failed(id, summary, remediation string, evidence map[string]any, class failureClass) Check {
	return Check{ID: id, Status: StatusFail, Severity: SeverityError, Summary: summary, Evidence: evidence, Remediation: remediation, failure: class}
}

func skipped(id, reason string) Check {
	return Check{
		ID: id, Status: StatusSkipped, Severity: SeverityInfo, Summary: "check was not run",
		Evidence: map[string]any{"reason": reason}, Remediation: "run 'moment doctor --deep --json' locally for this check",
	}
}

func (builder *reportBuilder) finish(cancelled bool) Report {
	builder.report.Counts = Counts{}
	for _, check := range builder.report.Checks {
		switch check.Status {
		case StatusPass:
			builder.report.Counts.Pass++
		case StatusWarning:
			builder.report.Counts.Warning++
		case StatusFail:
			builder.report.Counts.Fail++
		case StatusSkipped:
			builder.report.Counts.Skipped++
		}
	}
	switch {
	case cancelled:
		builder.report.Status = OverallCancelled
	case builder.report.Counts.Fail > 0:
		builder.report.Status = OverallUnhealthy
	case builder.report.Counts.Warning > 0:
		builder.report.Status = OverallWarning
	default:
		builder.report.Status = OverallHealthy
	}
	builder.report.FailureKind = builder.report.Failure()
	return builder.report
}

// Registry returns a copy for uniqueness/contract tests and adapters.
func Registry() []string { return append([]string(nil), registry...) }
