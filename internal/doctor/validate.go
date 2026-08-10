package doctor

import "fmt"

// ValidateReport rejects malformed or incompatible reports at transport
// boundaries. It deliberately validates the fixed registry and aggregate
// fields so an older or untrusted server cannot silently change CLI meaning.
func ValidateReport(report Report, expectedMode Mode) error {
	if report.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported doctor schema version %d", report.SchemaVersion)
	}
	if report.Mode != expectedMode {
		return fmt.Errorf("unexpected doctor mode %q", report.Mode)
	}
	if len(report.Checks) != len(registry) {
		return fmt.Errorf("doctor report has %d checks, expected %d", len(report.Checks), len(registry))
	}
	seen := make(map[string]struct{}, len(report.Checks))
	counts := Counts{}
	for position, check := range report.Checks {
		if check.ID != registry[position] {
			return fmt.Errorf("doctor check %d has unexpected id %q", position, check.ID)
		}
		if _, exists := seen[check.ID]; exists {
			return fmt.Errorf("duplicate doctor check id %q", check.ID)
		}
		seen[check.ID] = struct{}{}
		if check.Summary == "" || check.Remediation == "" || check.Evidence == nil {
			return fmt.Errorf("doctor check %q is missing bounded presentation fields", check.ID)
		}
		switch check.Status {
		case StatusPass:
			counts.Pass++
			if check.Severity != SeverityInfo {
				return fmt.Errorf("doctor check %q has invalid pass severity", check.ID)
			}
		case StatusWarning:
			counts.Warning++
			if check.Severity != SeverityWarning {
				return fmt.Errorf("doctor check %q has invalid warning severity", check.ID)
			}
		case StatusFail:
			counts.Fail++
			if check.Severity != SeverityError {
				return fmt.Errorf("doctor check %q has invalid failure severity", check.ID)
			}
		case StatusSkipped:
			counts.Skipped++
			if check.Severity != SeverityInfo {
				return fmt.Errorf("doctor check %q has invalid skipped severity", check.ID)
			}
		default:
			return fmt.Errorf("doctor check %q has invalid status %q", check.ID, check.Status)
		}
	}
	if counts != report.Counts {
		return fmt.Errorf("doctor report counts do not match checks")
	}
	if !validFailureKind(report.FailureKind) {
		return fmt.Errorf("doctor report has invalid failure kind %q", report.FailureKind)
	}
	switch report.Status {
	case OverallHealthy:
		if counts.Fail != 0 || counts.Warning != 0 || report.FailureKind != FailureNone {
			return fmt.Errorf("healthy doctor report has non-healthy checks")
		}
	case OverallWarning:
		if counts.Fail != 0 || counts.Warning == 0 || report.FailureKind != FailureNone {
			return fmt.Errorf("warning doctor report has inconsistent checks")
		}
	case OverallUnhealthy:
		if counts.Fail == 0 || report.FailureKind == FailureNone || report.FailureKind == FailureCancelled {
			return fmt.Errorf("unhealthy doctor report has inconsistent failure state")
		}
	case OverallCancelled:
		if report.FailureKind != FailureCancelled {
			return fmt.Errorf("cancelled doctor report has inconsistent failure state")
		}
	default:
		return fmt.Errorf("doctor report has invalid overall status %q", report.Status)
	}
	return nil
}

func validFailureKind(kind FailureKind) bool {
	switch kind {
	case FailureNone, FailureUnavailable, FailureIO, FailureIntegrity, FailureInternal, FailureCancelled:
		return true
	default:
		return false
	}
}
