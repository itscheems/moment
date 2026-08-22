// Package doctor owns deterministic, read-only runtime diagnostics.
package doctor

import (
	"context"
	"errors"
)

// SchemaVersion is the frozen machine report schema.
const SchemaVersion = 1

// Mode selects the safe summary or topology-sensitive deep registry.
type Mode string

// Supported diagnostic modes.
const (
	ModeSummary Mode = "summary"
	ModeDeep    Mode = "deep"
)

// Status is one check's stable result state.
type Status string

// Stable per-check statuses.
const (
	StatusPass    Status = "pass"
	StatusWarning Status = "warning"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// Severity is one check's remediation urgency.
type Severity string

// Stable per-check severities.
const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// OverallStatus summarizes all completed checks.
type OverallStatus string

// Stable report-level states.
const (
	OverallHealthy   OverallStatus = "healthy"
	OverallWarning   OverallStatus = "warning"
	OverallUnhealthy OverallStatus = "unhealthy"
	OverallCancelled OverallStatus = "cancelled"
)

// Stable check IDs in schema v1.
const (
	CheckDataDir                   = "data_dir"
	CheckDatabaseOpen              = "database_open"
	CheckCurrentGeneration         = "current_generation"
	CheckSchemaVersion             = "schema_version"
	CheckSQLiteIntegrity           = "sqlite_integrity"
	CheckForeignKeys               = "foreign_keys"
	CheckFTS5Capability            = "fts5_capability"
	CheckFTSConsistency            = "fts_consistency"
	CheckMediaDirectory            = "media_directory"
	CheckMissingMedia              = "missing_media"
	CheckOrphanMedia               = "orphan_media"
	CheckMediaReferenceConsistency = "media_reference_consistency"
	CheckMediaChecksum             = "media_checksum"
	CheckPreviewCache              = "preview_cache"
	CheckSecretPermissions         = "secret_permissions"
	CheckDiskSpace                 = "disk_space"
	CheckInstanceLocks             = "instance_locks"
	CheckStaleImportStaging        = "stale_import_staging"
	CheckCurrentNextState          = "current_next_state"
	CheckSiteConfiguration         = "site_configuration"
	CheckImportRecoveryState       = "import_recovery_state"
	CheckAccountIntegrity          = "account_integrity"
)

var registry = []string{
	CheckDataDir,
	CheckDatabaseOpen,
	CheckCurrentGeneration,
	CheckSchemaVersion,
	CheckSQLiteIntegrity,
	CheckForeignKeys,
	CheckFTS5Capability,
	CheckFTSConsistency,
	CheckMediaDirectory,
	CheckMissingMedia,
	CheckOrphanMedia,
	CheckMediaReferenceConsistency,
	CheckMediaChecksum,
	CheckPreviewCache,
	CheckSecretPermissions,
	CheckDiskSpace,
	CheckInstanceLocks,
	CheckStaleImportStaging,
	CheckCurrentNextState,
	CheckSiteConfiguration,
	CheckImportRecoveryState,
	CheckAccountIntegrity,
}

// Check is one stable, independently actionable diagnostic result.
type Check struct {
	ID          string         `json:"id"`
	Status      Status         `json:"status"`
	Severity    Severity       `json:"severity"`
	Summary     string         `json:"summary"`
	Evidence    map[string]any `json:"evidence"`
	Remediation string         `json:"remediation"`
	failure     failureClass
}

// Counts aggregates check statuses without requiring clients to recompute them.
type Counts struct {
	Pass    int `json:"pass"`
	Warning int `json:"warning"`
	Fail    int `json:"fail"`
	Skipped int `json:"skipped"`
}

// Report is doctor JSON schema v1. Registry order is stable and maps are used
// only for bounded evidence fields.
type Report struct {
	SchemaVersion int           `json:"schema_version"`
	Mode          Mode          `json:"mode"`
	Status        OverallStatus `json:"status"`
	FailureKind   FailureKind   `json:"failure_kind"`
	Checks        []Check       `json:"checks"`
	Counts        Counts        `json:"counts"`
}

// Progress reports one completed registry check without its evidence.
type Progress struct {
	Completed int
	Total     int
	CheckID   string
}

type failureClass uint8

const (
	failureNone failureClass = iota
	failureUnavailable
	failureIO
	failureIntegrity
	failureInternal
	failureCancelled
)

// FailureKind is adapter-facing and independent of CLI numeric exits.
type FailureKind string

// Stable adapter-facing failure classes.
const (
	FailureNone        FailureKind = ""
	FailureUnavailable FailureKind = "unavailable"
	FailureIO          FailureKind = "io"
	FailureIntegrity   FailureKind = "integrity"
	FailureInternal    FailureKind = "internal"
	FailureCancelled   FailureKind = "cancelled"
)

// Failure returns the traversal-order-independent dominant failure.
func (report Report) Failure() FailureKind {
	if report.Status == OverallCancelled {
		return FailureCancelled
	}
	if report.FailureKind != FailureNone {
		return report.FailureKind
	}
	dominant := failureNone
	for _, check := range report.Checks {
		if failurePriority(check.failure) > failurePriority(dominant) {
			dominant = check.failure
		}
	}
	switch dominant {
	case failureUnavailable:
		return FailureUnavailable
	case failureIO:
		return FailureIO
	case failureIntegrity:
		return FailureIntegrity
	case failureInternal:
		return FailureInternal
	case failureCancelled:
		return FailureCancelled
	default:
		return FailureNone
	}
}

func failurePriority(value failureClass) int {
	switch value {
	case failureCancelled:
		return 5
	case failureInternal:
		return 4
	case failureIntegrity:
		return 3
	case failureIO:
		return 2
	case failureUnavailable:
		return 1
	default:
		return 0
	}
}

func cancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
