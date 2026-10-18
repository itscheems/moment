package doctor

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

type evidenceKind uint8

const (
	evidenceBoolean evidenceKind = iota + 1
	evidenceUnsigned
	evidenceText
	evidenceTextList
)

var evidenceSchemas = map[string]map[string]evidenceKind{
	CheckDataDir:                   {"resolved": evidenceBoolean, "exists": evidenceBoolean, "directory": evidenceBoolean, "readable": evidenceBoolean, "traversable_permission": evidenceBoolean, "writable_permission": evidenceBoolean},
	CheckDatabaseOpen:              {"opened": evidenceBoolean, "bounded": evidenceBoolean, "read_only": evidenceBoolean, "query_only": evidenceBoolean, "cleanup_complete": evidenceBoolean},
	CheckCurrentGeneration:         {"instance_valid": evidenceBoolean, "generation_valid": evidenceBoolean, "valid": evidenceBoolean, "generation_id": evidenceText, "generation_readable": evidenceBoolean, "database_readable_permission": evidenceBoolean, "authority_permissions_valid": evidenceBoolean, "stable": evidenceBoolean},
	CheckSchemaVersion:             {"applied_version": evidenceUnsigned, "expected_version": evidenceUnsigned, "exact": evidenceBoolean},
	CheckSQLiteIntegrity:           {"problems": evidenceUnsigned, "bounded": evidenceBoolean},
	CheckForeignKeys:               {"violations": evidenceUnsigned, "bounded": evidenceBoolean},
	CheckFTS5Capability:            {"fts5": evidenceBoolean, "trigram": evidenceBoolean},
	CheckFTSConsistency:            {"checked": evidenceBoolean, "total": evidenceUnsigned, "drifted": evidenceUnsigned, "orphan_rows": evidenceUnsigned, "stored_version": evidenceUnsigned, "expected_version": evidenceUnsigned, "bounded": evidenceBoolean},
	CheckMediaDirectory:            {"originals_ready": evidenceBoolean, "preview_root_ready": evidenceBoolean},
	CheckMissingMedia:              {"missing_referenced": evidenceUnsigned},
	CheckOrphanMedia:               {"unreferenced_records": evidenceUnsigned, "orphan_blobs": evidenceUnsigned, "bounded": evidenceBoolean},
	CheckMediaReferenceConsistency: {"records": evidenceUnsigned, "bounded": evidenceBoolean, "resolver_ready": evidenceBoolean, "inconsistent": evidenceUnsigned},
	CheckMediaChecksum:             {"checked": evidenceUnsigned, "corrupt": evidenceUnsigned, "io_failures": evidenceUnsigned},
	CheckPreviewCache:              {"ready": evidenceUnsigned, "stale": evidenceUnsigned, "missing": evidenceUnsigned, "recipe_version": evidenceText},
	CheckSecretPermissions:         {"exists": evidenceBoolean, "permissions_valid": evidenceBoolean, "platform": evidenceText},
	CheckDiskSpace:                 {"measured": evidenceBoolean, "available_bytes": evidenceUnsigned, "warning_threshold_bytes": evidenceUnsigned},
	CheckInstanceLocks:             {"snapshot_acquired": evidenceBoolean, "permissions_valid": evidenceBoolean, "permission_issues": evidenceTextList, "serve_state": evidenceText, "legacy_uninitialized": evidenceTextList, "snapshot_order": evidenceText},
	CheckStaleImportStaging:        {"total": evidenceUnsigned, "ready": evidenceUnsigned, "expired": evidenceUnsigned, "committing": evidenceUnsigned, "used": evidenceUnsigned, "failed": evidenceUnsigned, "invalid": evidenceUnsigned, "incomplete": evidenceUnsigned, "bounded": evidenceBoolean},
	CheckCurrentNextState:          {"present": evidenceBoolean, "safe_regular_file": evidenceBoolean, "permissions_valid": evidenceBoolean},
	CheckSiteConfiguration:         {"valid": evidenceBoolean},
	CheckAccountIntegrity:          {"active_administrators": evidenceUnsigned, "profile_resolves": evidenceBoolean, "orphan_identities": evidenceUnsigned, "orphan_sessions": evidenceUnsigned, "orphan_comment_authors": evidenceUnsigned, "reserved_usernames": evidenceUnsigned},
	CheckArticleIntegrity:          {"articles": evidenceUnsigned, "bounded": evidenceBoolean, "missing_bodies": evidenceUnsigned, "orphan_bodies": evidenceUnsigned, "unparsable_bodies": evidenceUnsigned, "drifted_projections": evidenceUnsigned, "stale_excerpts": evidenceUnsigned},
	CheckImportRecoveryState:       {"journals": evidenceUnsigned, "active": evidenceUnsigned, "needs_attention": evidenceUnsigned, "current_mismatch": evidenceUnsigned, "invalid": evidenceUnsigned, "latest_phase": evidenceText, "bounded": evidenceBoolean},
}

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
		if len(check.Summary) > 512 || len(check.Remediation) > 512 {
			return fmt.Errorf("doctor check %q presentation exceeds its byte bound", check.ID)
		}
		if err := validateEvidence(check); err != nil {
			return err
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

func validateEvidence(check Check) error {
	if len(check.Evidence) < 1 || len(check.Evidence) > 16 {
		return fmt.Errorf("doctor check %q evidence exceeds its field bound", check.ID)
	}
	if check.Status == StatusSkipped {
		reason, ok := check.Evidence["reason"]
		if len(check.Evidence) != 1 || !ok || !validEvidenceValue(reason, evidenceText) {
			return fmt.Errorf("doctor check %q has invalid skipped evidence", check.ID)
		}
		return nil
	}
	schema, ok := evidenceSchemas[check.ID]
	if !ok {
		return fmt.Errorf("doctor check %q has no evidence schema", check.ID)
	}
	for key, value := range check.Evidence {
		kind, known := schema[key]
		if !known || !validEvidenceValue(value, kind) {
			return fmt.Errorf("doctor check %q has invalid evidence field %q", check.ID, key)
		}
	}
	return nil
}

func validEvidenceValue(value any, kind evidenceKind) bool {
	if value == nil {
		return false
	}
	switch kind {
	case evidenceBoolean:
		return reflect.TypeOf(value).Kind() == reflect.Bool
	case evidenceText:
		reflection := reflect.ValueOf(value)
		return reflection.Kind() == reflect.String && reflection.Len() <= 512
	case evidenceUnsigned:
		if number, ok := value.(json.Number); ok {
			text := number.String()
			if text == "" || len(text) > 20 {
				return false
			}
			_, err := strconv.ParseUint(text, 10, 64)
			return err == nil
		}
		reflection := reflect.ValueOf(value)
		switch reflection.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflection.Int() >= 0
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return true
		case reflect.Float32, reflect.Float64:
			number := reflection.Float()
			return number >= 0 && number <= 1<<53-1 && math.Trunc(number) == number
		default:
			return false
		}
	case evidenceTextList:
		reflection := reflect.ValueOf(value)
		if reflection.Kind() != reflect.Slice || reflection.Len() > 16 {
			return false
		}
		for index := 0; index < reflection.Len(); index++ {
			item := reflection.Index(index)
			if item.Kind() == reflect.Interface {
				item = item.Elem()
			}
			if !item.IsValid() || item.Kind() != reflect.String || item.Len() > 128 {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validFailureKind(kind FailureKind) bool {
	switch kind {
	case FailureNone, FailureUnavailable, FailureIO, FailureIntegrity, FailureInternal, FailureCancelled:
		return true
	default:
		return false
	}
}
