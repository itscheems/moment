// Package domain contains Moment's transport-independent product rules.
package domain

import (
	"errors"
	"fmt"
)

// Code is a stable machine-readable domain failure identifier.
type Code string

// Stable error codes shared by application, HTTP, and CLI adapters.
const (
	CodeInvalidArgument             Code = "INVALID_ARGUMENT"
	CodeContentEmpty                Code = "CONTENT_EMPTY"
	CodeContentTooLarge             Code = "CONTENT_TOO_LARGE"
	CodeExternalImageNotAllowed     Code = "EXTERNAL_IMAGE_NOT_ALLOWED"
	CodePostNotFound                Code = "POST_NOT_FOUND"
	CodeCommentNotFound             Code = "COMMENT_NOT_FOUND"
	CodeCommentDepthExceeded        Code = "COMMENT_DEPTH_EXCEEDED"
	CodeCommentsDisabled            Code = "COMMENTS_DISABLED"
	CodeRateLimited                 Code = "RATE_LIMITED"
	CodeMediaTypeUnsupported        Code = "MEDIA_TYPE_UNSUPPORTED"
	CodeMediaTooLarge               Code = "MEDIA_TOO_LARGE"
	CodeMediaDimensionsExceeded     Code = "MEDIA_DIMENSIONS_EXCEEDED"
	CodeMediaCorrupt                Code = "MEDIA_CORRUPT"
	CodeMediaPreviewFailed          Code = "MEDIA_PREVIEW_FAILED"
	CodeUnauthorized                Code = "UNAUTHORIZED"
	CodeIdempotencyKeyReused        Code = "IDEMPOTENCY_KEY_REUSED"
	CodeImportInProgress            Code = "IMPORT_IN_PROGRESS"
	CodeInstanceInUse               Code = "INSTANCE_IN_USE"
	CodeModeUnsupported             Code = "MODE_UNSUPPORTED"
	CodeImportDiskSpaceInsufficient Code = "IMPORT_DISK_SPACE_INSUFFICIENT"
	CodeImportStagingInvalid        Code = "IMPORT_STAGING_INVALID"
	CodeImportStagingExpired        Code = "IMPORT_STAGING_EXPIRED"
	CodeExportInvalid               Code = "EXPORT_INVALID"
	CodeExportVersionUnsupported    Code = "EXPORT_VERSION_UNSUPPORTED"
	CodeExportChecksumMismatch      Code = "EXPORT_CHECKSUM_MISMATCH"
	CodeDataIntegrityFailed         Code = "DATA_INTEGRITY_FAILED"
	CodeMaintenanceMode             Code = "MAINTENANCE_MODE"
	CodeInternal                    Code = "INTERNAL"
)

// Error is safe to map into CLI and HTTP error envelopes. Details must contain
// identifiers or limits, never user content or secrets.
type Error struct {
	Code    Code
	Message string
	Details map[string]any
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError constructs a stable domain error without transport concerns.
func NewError(code Code, message string, details map[string]any) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

// HasCode reports whether err or a wrapped error carries code.
func HasCode(err error, code Code) bool {
	var domainErr *Error
	return errors.As(err, &domainErr) && domainErr.Code == code
}
