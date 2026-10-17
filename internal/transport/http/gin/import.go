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
	"strconv"
	"strings"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

// ImportInspector is the transport-independent hostile archive preflight.
type ImportInspector interface {
	Inspect(context.Context, io.Reader, archive.Authorization) (archive.Inspection, error)
}

type importPlanner interface {
	Plan(context.Context, io.Reader, archive.Authorization) (archive.ImportPlan, error)
}

// ImportCommitter exposes token consumption and durable status without
// coupling Gin to live-bundle maintenance implementation.
type ImportCommitter interface {
	Commit(context.Context, string, archive.Authorization) (archive.CommitResult, error)
	Status(context.Context) (archive.CommitStatus, error)
}

type archiveImportCommitter interface {
	CommitArchive(context.Context, io.Reader, string, archive.Authorization) (archive.CommitResult, error)
}

type mergeImportCommitter interface {
	InspectMergeArchive(context.Context, io.Reader, archive.Authorization) (archive.MergeInspection, error)
	CommitMerge(context.Context, string, archive.Authorization, archive.MergeSelection) (archive.CommitResult, error)
	DiscardMerge(context.Context, string, archive.Authorization) error
}

type reviewImportCommitter interface {
	InspectReviewArchive(context.Context, io.Reader, archive.Authorization) (archive.MergeInspection, error)
	CommitReviewedReplacement(context.Context, string, archive.Authorization, string) (archive.CommitResult, error)
	ReadReviewMedia(context.Context, string, archive.Authorization, string, archive.MergeReviewSource, string) (media.Asset, error)
}

// RegisterImportInspectRoutes installs the owner-only raw ZIP upload endpoint.
func RegisterImportInspectRoutes(engine *gin.Engine, inspector ImportInspector, gate *Gate) {
	backups := engine.Group("/api/v1/backups")
	backups.Use(gate.RequireAdmin())
	backups.POST("/import/inspect", inspectImport(inspector, gate))
	if planner, ok := inspector.(importPlanner); ok {
		backups.POST("/import/plan", planImport(planner, gate))
	}
}

// RegisterImportCommitRoutes installs the owner-only destructive commit and
// polling status endpoints. Status intentionally accepts no token.
func RegisterImportCommitRoutes(engine *gin.Engine, committer ImportCommitter, gate *Gate) {
	backups := engine.Group("/api/v1/backups")
	backups.Use(gate.RequireAdmin())
	backups.POST("/import/commit", commitImport(committer, gate))
	if restorer, ok := committer.(archiveImportCommitter); ok {
		backups.POST("/import/restore", restoreImport(restorer, gate))
	}
	if merger, ok := committer.(mergeImportCommitter); ok {
		backups.POST("/import/merge/plan", planMergeImport(merger, gate))
		backups.POST("/import/merge/commit", commitMergeImport(merger, gate))
		backups.POST("/import/merge/discard", discardMergeImport(merger, gate))
	}
	if reviewer, ok := committer.(reviewImportCommitter); ok {
		backups.POST("/import/review", reviewImport(reviewer, gate))
		backups.POST("/import/review/replace", commitReviewedReplacement(reviewer, gate))
		backups.POST("/import/review/media", getReviewMedia(reviewer, gate))
	}
	backups.GET("/import/status", importStatus(committer))
}

func reviewImport(reviewer reviewImportCommitter, gate *Gate) gin.HandlerFunc {
	return importArchiveUpload(gate, "review", false, func(ctx context.Context, source io.Reader, _ string, epoch archive.Authorization) (any, error) {
		return reviewer.InspectReviewArchive(ctx, source, epoch)
	}, http.StatusCreated)
}

func commitReviewedReplacement(reviewer reviewImportCommitter, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !requireImportJSON(ctx, "review replacement") {
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		token, planID, err := decodeReviewIdentityRequest(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import review replacement requires exactly staging_token and plan_id", nil)
			return
		}
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
			return
		}
		result, err := reviewer.CommitReviewedReplacement(ctx.Request.Context(), token, epoch, planID)
		if err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, result, nil)
	}
}

func getReviewMedia(reviewer reviewImportCommitter, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !requireImportJSON(ctx, "review media") {
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		token, planID, source, mediaID, err := decodeReviewMediaRequest(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import review media requires exactly staging_token, plan_id, source and media_id", nil)
			return
		}
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
			return
		}
		asset, err := reviewer.ReadReviewMedia(ctx.Request.Context(), token, epoch, planID, source, mediaID)
		if err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		ctx.Header("Content-Type", asset.MIMEType)
		ctx.Header("Cache-Control", "private, no-store")
		ctx.Header("ETag", asset.ETag)
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("Content-Length", strconv.Itoa(len(asset.Contents)))
		ctx.Status(http.StatusOK)
		_, _ = ctx.Writer.Write(asset.Contents)
	}
}

func requireImportJSON(ctx *gin.Context, operation string) bool {
	if ctx.Request.URL.RawQuery != "" {
		Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import "+operation+" does not accept query parameters", map[string]any{"field": "query"})
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import "+operation+" requires application/json", map[string]any{"field": "content_type"})
		return false
	}
	return true
}

func authorizedImportSubject(ctx *gin.Context, gate *Gate) (archive.Authorization, bool) {
	user, ok := CurrentAccount(ctx)
	if !ok {
		unauthorized(ctx)
		return archive.Authorization{}, false
	}
	credential, ok := CurrentCredentialID(ctx)
	if !ok {
		unauthorized(ctx)
		return archive.Authorization{}, false
	}
	return archive.Authorization{AccountID: user.ID, CredentialID: credential}, true
}

func planMergeImport(merger mergeImportCommitter, gate *Gate) gin.HandlerFunc {
	return importArchiveUpload(gate, "merge plan", false, func(ctx context.Context, source io.Reader, _ string, epoch archive.Authorization) (any, error) {
		return merger.InspectMergeArchive(ctx, source, epoch)
	}, http.StatusCreated)
}

func restoreImport(restorer archiveImportCommitter, gate *Gate) gin.HandlerFunc {
	return importArchiveUpload(gate, "restore", true, func(ctx context.Context, source io.Reader, expectedArchiveSHA string, epoch archive.Authorization) (any, error) {
		return restorer.CommitArchive(ctx, source, expectedArchiveSHA, epoch)
	}, http.StatusOK)
}

func planImport(planner importPlanner, gate *Gate) gin.HandlerFunc {
	return importArchiveUpload(gate, "plan", false, func(ctx context.Context, source io.Reader, _ string, epoch archive.Authorization) (any, error) {
		return planner.Plan(ctx, source, epoch)
	}, http.StatusOK)
}

func importArchiveUpload(gate *Gate, operation string, requireExpectedSHA bool, run func(context.Context, io.Reader, string, archive.Authorization) (any, error), successStatus int) gin.HandlerFunc {
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
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
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
	case domain.CodeImportMergeUnsupported:
		// The archive is fine and the request is well formed; this site holds
		// something the comparison cannot reconcile, and the error names the
		// path that can.
		status = http.StatusUnprocessableEntity
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

func commitMergeImport(committer mergeImportCommitter, gate *Gate) gin.HandlerFunc {
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
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
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

func discardMergeImport(committer mergeImportCommitter, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import merge discard does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import merge discard requires application/json", map[string]any{"field": "content_type"})
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		token, err := decodeCommitRequest(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import merge discard requires exactly one non-empty staging_token", map[string]any{"field": "staging_token"})
			return
		}
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
			return
		}
		if err := committer.DiscardMerge(ctx.Request.Context(), token, epoch); err != nil {
			writeImportCommitError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, gin.H{"discarded": true}, nil)
	}
}

func commitImport(committer ImportCommitter, gate *Gate) gin.HandlerFunc {
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
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
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

func decodeReviewIdentityRequest(body []byte) (string, string, error) {
	values, err := decodeReviewRequest(body, map[string]bool{"staging_token": true, "plan_id": true})
	if err != nil || !validReviewCredential(values["staging_token"], 256) || !canonicalArchiveSHA(values["plan_id"]) {
		return "", "", errors.New("review identity is invalid")
	}
	return values["staging_token"], values["plan_id"], nil
}

func decodeReviewMediaRequest(body []byte) (string, string, archive.MergeReviewSource, string, error) {
	values, err := decodeReviewRequest(body, map[string]bool{"staging_token": true, "plan_id": true, "source": true, "media_id": true})
	source := archive.MergeReviewSource(values["source"])
	if err != nil || !validReviewCredential(values["staging_token"], 256) || !canonicalArchiveSHA(values["plan_id"]) ||
		(source != archive.MergeReviewLocal && source != archive.MergeReviewIncoming) || !validReviewCredential(values["media_id"], 64) {
		return "", "", "", "", errors.New("review media identity is invalid")
	}
	return values["staging_token"], values["plan_id"], source, values["media_id"], nil
}

func decodeReviewRequest(body []byte, expected map[string]bool) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("request must be one object")
	}
	values := make(map[string]string, len(expected))
	for decoder.More() {
		name, err := decoder.Token()
		field, ok := name.(string)
		if err != nil || !ok || !expected[field] {
			return nil, errors.New("request has an unknown field")
		}
		if _, duplicate := values[field]; duplicate {
			return nil, errors.New("request has a duplicate field")
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("request field is not a string")
		}
		values[field] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF || len(values) != len(expected) {
		return nil, errors.New("request object is incomplete")
	}
	return values, nil
}

func validReviewCredential(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
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
	case domain.CodeImportMergeUnsupported:
		// The archive is fine and the request is well formed; this site holds
		// something the comparison cannot reconcile, and the error names the
		// path that can.
		status = http.StatusUnprocessableEntity
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

func inspectImport(inspector ImportInspector, gate *Gate) gin.HandlerFunc {
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
		epoch, ok := authorizedImportSubject(ctx, gate)
		if !ok {
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
