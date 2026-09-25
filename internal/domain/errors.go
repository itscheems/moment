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
	CodeCommentRejected             Code = "COMMENT_REJECTED"
	CodeRateLimited                 Code = "RATE_LIMITED"
	CodeMediaTypeUnsupported        Code = "MEDIA_TYPE_UNSUPPORTED"
	CodeMediaTooLarge               Code = "MEDIA_TOO_LARGE"
	CodeMediaDimensionsExceeded     Code = "MEDIA_DIMENSIONS_EXCEEDED"
	CodeMediaCorrupt                Code = "MEDIA_CORRUPT"
	CodeMediaPreviewFailed          Code = "MEDIA_PREVIEW_FAILED"
	CodeMediaProcessorUnavailable   Code = "MEDIA_PROCESSOR_UNAVAILABLE"
	CodeMediaInUse                  Code = "MEDIA_IN_USE"
	CodeMediaNotFound               Code = "MEDIA_NOT_FOUND"
	CodeLocationProviderUnavailable Code = "LOCATION_PROVIDER_UNAVAILABLE"
	CodeLinkPreviewUnavailable      Code = "LINK_PREVIEW_UNAVAILABLE"
	CodeUnauthorized                Code = "UNAUTHORIZED"
	CodeAccountNotFound             Code = "ACCOUNT_NOT_FOUND"
	CodeAccountSuspended            Code = "ACCOUNT_SUSPENDED"
	CodeAccountNotApproved          Code = "ACCOUNT_NOT_APPROVED"
	CodeUsernameTaken               Code = "USERNAME_TAKEN"
	CodeUsernameReserved            Code = "USERNAME_RESERVED"
	CodeRegistrationClosed          Code = "REGISTRATION_CLOSED"
	CodeIdentityAlreadyLinked       Code = "IDENTITY_ALREADY_LINKED"
	CodeIdentityNotLinked           Code = "IDENTITY_NOT_LINKED"
	CodeLastCredential              Code = "LAST_CREDENTIAL"
	// CodeCredentialUnavailable means the account exists and does not sign in
	// the way the caller offered. Its details carry the kinds that would work,
	// so a client can say what to do instead rather than guessing that a
	// third-party provider is the answer.
	CodeCredentialUnavailable    Code = "CREDENTIAL_UNAVAILABLE"
	CodeOAuthProviderUnavailable Code = "OAUTH_PROVIDER_UNAVAILABLE"
	CodeOAuthStateInvalid        Code = "OAUTH_STATE_INVALID"
	CodeOAuthExchangeFailed      Code = "OAUTH_EXCHANGE_FAILED"
	CodeOAuthIdentityUnusable    Code = "OAUTH_IDENTITY_UNUSABLE"
	CodeOAuthDeviceExpired       Code = "OAUTH_DEVICE_EXPIRED"
	CodeOAuthDeviceDenied        Code = "OAUTH_DEVICE_DENIED"
	CodeIdempotencyKeyReused     Code = "IDEMPOTENCY_KEY_REUSED"
	CodeArticleDraftNotFound     Code = "ARTICLE_DRAFT_NOT_FOUND"
	// CodeArticleDraftStale means somebody else saved the same draft since
	// this writer last read it. Two tabs on one draft is ordinary; the answer
	// is to say so rather than to let one of them disappear.
	CodeArticleDraftStale           Code = "ARTICLE_DRAFT_STALE"
	CodeArticleDraftLimit           Code = "ARTICLE_DRAFT_LIMIT"
	CodeImportInProgress            Code = "IMPORT_IN_PROGRESS"
	CodeInstanceInUse               Code = "INSTANCE_IN_USE"
	CodeModeUnsupported             Code = "MODE_UNSUPPORTED"
	CodeImportDiskSpaceInsufficient Code = "IMPORT_DISK_SPACE_INSUFFICIENT"
	CodeImportStagingInvalid        Code = "IMPORT_STAGING_INVALID"
	CodeImportStagingExpired        Code = "IMPORT_STAGING_EXPIRED"
	CodeImportOwnerMismatch         Code = "IMPORT_OWNER_MISMATCH"
	CodeImportMergePlanStale        Code = "IMPORT_MERGE_PLAN_STALE"
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
