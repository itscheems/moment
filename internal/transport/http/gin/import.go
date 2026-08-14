package ginhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

// ImportInspector is the transport-independent hostile archive preflight.
type ImportInspector interface {
	Inspect(context.Context, io.Reader, uint64) (archive.Inspection, error)
}

type importPlanner interface {
	Plan(context.Context, io.Reader, uint64) (archive.ImportPlan, error)
}

// ImportCommitter exposes token consumption and durable status without
// coupling Gin to live-bundle maintenance implementation.
type ImportCommitter interface {
	Commit(context.Context, string, uint64) (archive.CommitResult, error)
	Status(context.Context) (archive.CommitStatus, error)
}

type archiveImportCommitter interface {
	CommitArchive(context.Context, io.Reader, string, uint64) (archive.CommitResult, error)
}

type mergeImportCommitter interface {
	InspectMergeArchive(context.Context, io.Reader, uint64) (archive.MergeInspection, error)
	CommitMerge(context.Context, string, uint64, archive.MergeSelection) (archive.CommitResult, error)
}

// RegisterImportInspectRoutes installs the owner-only raw ZIP upload endpoint.
func RegisterImportInspectRoutes(engine *gin.Engine, inspector ImportInspector, manager *auth.Manager) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/import/inspect", inspectImport(inspector, manager))
	if planner, ok := inspector.(importPlanner); ok {
		owner.POST("/import/plan", planImport(planner, manager))
	}
}

// RegisterImportCommitRoutes installs the owner-only destructive commit and
// polling status endpoints. Status intentionally accepts no token.
func RegisterImportCommitRoutes(engine *gin.Engine, committer ImportCommitter, manager *auth.Manager) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/import/commit", commitImport(committer, manager))
	if restorer, ok := committer.(archiveImportCommitter); ok {
		owner.POST("/import/restore", restoreImport(restorer, manager))
	}
	if merger, ok := committer.(mergeImportCommitter); ok {
		owner.POST("/import/merge/plan", planMergeImport(merger, manager))
		owner.POST("/import/merge/commit", commitMergeImport(merger, manager))
	}
	owner.GET("/import/status", importStatus(committer))
}

func planMergeImport(merger mergeImportCommitter, manager *auth.Manager) gin.HandlerFunc {
	return importArchiveUpload(manager, "merge plan", false, func(ctx context.Context, source io.Reader, _ string, epoch uint64) (any, error) {
		return merger.InspectMergeArchive(ctx, source, epoch)
	}, http.StatusCreated)
}

func restoreImport(restorer archiveImportCommitter, manager *auth.Manager) gin.HandlerFunc {
	return importArchiveUpload(manager, "restore", true, func(ctx context.Context, source io.Reader, expectedArchiveSHA string, epoch uint64) (any, error) {
		return restorer.CommitArchive(ctx, source, expectedArchiveSHA, epoch)
	}, http.StatusOK)
}

func planImport(planner importPlanner, manager *auth.Manager) gin.HandlerFunc {
	return importArchiveUpload(manager, "plan", false, func(ctx context.Context, source io.Reader, _ string, epoch uint64) (any, error) {
		return planner.Plan(ctx, source, epoch)
	}, http.StatusOK)
}

func importArchiveUpload(manager *auth.Manager, operation string, requireExpectedSHA bool, run func(context.Context, io.Reader, string, uint64) (any, error), successStatus int) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import "+operation+" does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/zip" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import "+operation+" requires application/zip", map[string]any{"field": "content_type"})
			return
		}
		expectedArchiveSHA := ""
		if requireExpectedSHA {
			values := ctx.Request.Header.Values("X-Moment-Archive-SHA256")
			if len(values) != 1 || !canonicalArchiveSHA(values[0]) {
				Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import restore requires one canonical expected archive SHA-256", map[string]any{"field": "x_moment_archive_sha256"})
				return
			}
			expectedArchiveSHA = values[0]
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		epoch, err := manager.SessionEpochAuthorized(grant)
		if err != nil {
			unauthorized(ctx)
			return
		}
		result, err := run(ctx.Request.Context(), ctx.Request.Body, expectedArchiveSHA, epoch)
		if err == nil {
			Success(ctx, successStatus, result, nil)
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			limit, _ := ctx.Get(bodyLimitKey)
			bodyTooLarge(ctx, limit.(int64))
			return
		}
		writeImportUploadError(ctx, operation, err)
	}
}

func canonicalArchiveSHA(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func writeImportUploadError(ctx *gin.Context, operation string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "TIMEOUT", "import "+operation+" timed out", nil)
		return
	}
	if ctx.Request.Context().Err() != nil || errors.Is(err, context.Canceled) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "import "+operation+" was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "import "+operation+" could not complete", nil)
		return
	}
	status := http.StatusUnprocessableEntity
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		status = http.StatusBadRequest
	case domain.CodeUnauthorized:
		status = http.StatusUnauthorized
	case domain.CodeImportInProgress, domain.CodeImportStagingInvalid, domain.CodeImportMergePlanStale, domain.CodeInstanceInUse:
		status = http.StatusConflict
	case domain.CodeImportStagingExpired:
		status = http.StatusGone
	case domain.CodeImportDiskSpaceInsufficient:
		status = http.StatusInsufficientStorage
	case domain.CodeMaintenanceMode:
		status = http.StatusServiceUnavailable
	case domain.CodeDataIntegrityFailed:
		status = http.StatusInternalServerError
	}
	Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
}

func commitMergeImport(committer mergeImportCommitter, manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import merge commit does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import merge commit requires application/json", map[string]any{"field": "content_type"})
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		token, selection, err := decodeMergeCommitRequest(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import merge commit requires exactly staging_token and selection", nil)
			return
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		epoch, err := manager.SessionEpochAuthorized(grant)
		if err != nil {
			unauthorized(ctx)
			return
		}
		result, err := committer.CommitMerge(ctx.Request.Context(), token, epoch, selection)
		if err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, result, nil)
	}
}

func commitImport(committer ImportCommitter, manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import commit does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import commit requires application/json", map[string]any{"field": "content_type"})
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		token, err := decodeCommitRequest(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import commit requires exactly one non-empty staging_token", map[string]any{"field": "staging_token"})
			return
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		epoch, err := manager.SessionEpochAuthorized(grant)
		if err != nil {
			unauthorized(ctx)
			return
		}
		result, err := committer.Commit(ctx.Request.Context(), token, epoch)
		if err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, result, nil)
	}
}

func importStatus(committer ImportCommitter) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import status does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		status, err := committer.Status(ctx.Request.Context())
		if err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, status, nil)
	}
}

func decodeCommitRequest(body []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", errors.New("request must be one object")
	}
	if !decoder.More() {
		return "", errors.New("staging_token is required")
	}
	name, err := decoder.Token()
	if err != nil || name != "staging_token" {
		return "", errors.New("unexpected request field")
	}
	var stagingToken string
	if err := decoder.Decode(&stagingToken); err != nil || stagingToken == "" || len(stagingToken) > 256 {
		return "", errors.New("staging_token is invalid")
	}
	if decoder.More() {
		return "", errors.New("request has duplicate or unknown fields")
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", errors.New("request object is incomplete")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("request contains multiple documents")
	}
	return stagingToken, nil
}

func decodeMergeCommitRequest(body []byte) (string, archive.MergeSelection, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", archive.MergeSelection{}, errors.New("request must be one object")
	}
	var stagingToken string
	var selection archive.MergeSelection
	seen := map[string]bool{}
	for decoder.More() {
		name, err := decoder.Token()
		field, ok := name.(string)
		if err != nil || !ok || seen[field] || (field != "staging_token" && field != "selection") {
			return "", archive.MergeSelection{}, errors.New("request has duplicate or unknown fields")
		}
		seen[field] = true
		switch field {
		case "staging_token":
			if err := decoder.Decode(&stagingToken); err != nil || stagingToken == "" || len(stagingToken) > 256 || strings.TrimSpace(stagingToken) != stagingToken {
				return "", archive.MergeSelection{}, errors.New("staging_token is invalid")
			}
		case "selection":
			if err := decoder.Decode(&selection); err != nil {
				return "", archive.MergeSelection{}, errors.New("selection is invalid")
			}
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF || !seen["staging_token"] || !seen["selection"] {
		return "", archive.MergeSelection{}, errors.New("request object is incomplete")
	}
	return stagingToken, selection, nil
}

func writeImportCommitError(ctx *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "TIMEOUT", "import commit timed out", nil)
		return
	}
	if ctx.Request.Context().Err() != nil || errors.Is(err, context.Canceled) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "import commit was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "import commit could not complete", nil)
		return
	}
	status := http.StatusUnprocessableEntity
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		status = http.StatusBadRequest
	case domain.CodeUnauthorized:
		status = http.StatusUnauthorized
	case domain.CodeImportInProgress, domain.CodeImportStagingInvalid, domain.CodeImportMergePlanStale, domain.CodeInstanceInUse:
		status = http.StatusConflict
	case domain.CodeImportStagingExpired:
		status = http.StatusGone
	case domain.CodeImportDiskSpaceInsufficient:
		status = http.StatusInsufficientStorage
	case domain.CodeMaintenanceMode:
		status = http.StatusServiceUnavailable
	case domain.CodeDataIntegrityFailed:
		status = http.StatusInternalServerError
	}
	Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
}

func inspectImport(inspector ImportInspector, manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import inspection does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/zip" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import inspection requires application/zip", map[string]any{"field": "content_type"})
			return
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		epoch, err := manager.SessionEpochAuthorized(grant)
		if err != nil {
			unauthorized(ctx)
			return
		}
		result, err := inspector.Inspect(ctx.Request.Context(), ctx.Request.Body, epoch)
		if err == nil {
			Success(ctx, http.StatusCreated, result, nil)
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			limit, _ := ctx.Get(bodyLimitKey)
			bodyTooLarge(ctx, limit.(int64))
			return
		}
		if ctx.Request.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "import inspection was cancelled", nil)
			return
		}
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			status := http.StatusUnprocessableEntity
			switch domainErr.Code {
			case domain.CodeInvalidArgument:
				status = http.StatusBadRequest
			case domain.CodeUnauthorized:
				status = http.StatusUnauthorized
			case domain.CodeImportInProgress, domain.CodeImportStagingInvalid:
				status = http.StatusConflict
			case domain.CodeImportStagingExpired:
				status = http.StatusGone
			case domain.CodeImportDiskSpaceInsufficient:
				status = http.StatusInsufficientStorage
			}
			Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "import inspection could not complete", nil)
	}
}
