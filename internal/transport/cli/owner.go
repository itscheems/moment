package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	maxTokenFileBytes = 4 << 10
	maxAPIResponse    = 1 << 20
)

type apiFailure struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"request_id"`
	} `json:"error"`
}

func registerOwner(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"owner", "init"},
		Short:      "Initialize the single owner credential",
		Long:       "Initialize the local Moment instance and create its single owner credential offline.",
		Inputs:     "A local --data-dir. No token input is accepted; a cryptographically random token is generated.",
		Output:     "The bearer token is shown exactly once with safe owner metadata. Save it immediately.",
		Errors:     "An active server returns INSTANCE_IN_USE; existing credentials return OWNER_ALREADY_INITIALIZED.",
		Safety:     "Offline only. The command exclusively owns serve.lock and never overwrites an existing owner.",
		Example:    "moment owner init --data-dir ./moment-data --json",
		Capability: CapabilityDirectOnly,
		Run:        runOwnerInit,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"owner", "status"},
		Short:      "Inspect safe owner authentication status",
		Long:       "Inspect initialization and revocation epoch without exposing credential or signing material.",
		Inputs:     "Direct mode reads the local instance. API mode authenticates with --token-file or MOMENT_TOKEN.",
		Output:     "Equivalent initialized, session_epoch, and safe timestamp fields in direct and API modes.",
		Errors:     "Invalid API credentials return UNAUTHORIZED; invalid secret state fails closed.",
		Example:    "moment owner status --data-dir ./moment-data --json",
		Capability: CapabilityBoth,
		Run:        runOwnerStatus,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"owner", "rotate-token"},
		Short:      "Rotate and revoke the owner credential",
		Long:       "Generate a new owner bearer token and atomically revoke every old bearer token and browser session.",
		Inputs:     "Direct mode requires a stopped server. API mode reads the current token from --token-file or MOMENT_TOKEN.",
		Output:     "The new token is shown exactly once with the incremented session epoch.",
		Errors:     "Direct mode returns INSTANCE_IN_USE for an active server; API mode returns UNAUTHORIZED for invalid credentials.",
		Safety:     "Direct rotation exclusively owns serve.lock; API rotation is committed by the running server.",
		Example:    "moment --url http://127.0.0.1:8080 --token-file ./owner.token owner rotate-token --json",
		Capability: CapabilityBoth,
		Run:        runOwnerRotate,
	})
}

func runOwnerInit(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: target.DataDirectory()})
	if err != nil {
		return Result{}, ownerRuntimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return Result{}, NewCommandError(
			"DATABASE_NOT_READY",
			"the Moment database could not be initialized",
			"inspect the selected data directory and retry",
			ExitIntegrity,
		).WithCause(err)
	}
	if err := database.Close(); err != nil {
		return Result{}, err
	}
	credential, err := auth.InitializeOffline(ctx, runtimeState, auth.Options{})
	if err != nil {
		return Result{}, ownerAuthError(err)
	}
	return ownerCredentialResult("Owner initialized", credential), nil
}

func runOwnerStatus(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
		if err != nil {
			return Result{}, ownerRuntimeError(err)
		}
		status, err := auth.ReadStatus(runtimeState.Root)
		if err != nil {
			return Result{}, ownerAuthError(err)
		}
		return ownerStatusResult(status), nil
	case ModeAPI:
		var status auth.Status
		meta, err := ownerAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/owner/status", &status)
		if err != nil {
			return Result{}, err
		}
		result := ownerStatusResult(status)
		result.Meta = meta
		return result, nil
	default:
		return Result{}, errors.New("unsupported owner status transport")
	}
}

func runOwnerRotate(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
		if err != nil {
			return Result{}, ownerRuntimeError(err)
		}
		credential, err := auth.RotateOffline(ctx, runtimeState, auth.Options{})
		if err != nil {
			return Result{}, ownerAuthError(err)
		}
		return ownerCredentialResult("Owner token rotated", credential), nil
	case ModeAPI:
		var credential auth.Credential
		meta, err := ownerAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/token/rotate", &credential)
		if err != nil {
			return Result{}, err
		}
		result := ownerCredentialResult("Owner token rotated", credential)
		result.Meta = meta
		return result, nil
	default:
		return Result{}, errors.New("unsupported owner rotation transport")
	}
}

func ownerCredentialResult(action string, credential auth.Credential) Result {
	return Result{
		Data: credential,
		Text: fmt.Sprintf("%s.\nToken: %s\nStore this token now; it will not be shown again.", action, credential.Token),
	}
}

func ownerStatusResult(status auth.Status) Result {
	text := "Owner authentication is not initialized"
	if status.Initialized {
		text = fmt.Sprintf("Owner authentication is initialized (session epoch %d)", status.SessionEpoch)
	}
	return Result{Data: status, Text: text}
}

func ownerAPIRequest(ctx context.Context, invocation Invocation, method, endpoint string, destination any) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpointURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAPIResponse {
		return nil, NewCommandError(
			"DEPENDENCY_INVALID_RESPONSE",
			"the Moment API response exceeded the allowed size",
			"check the server and reverse proxy configuration",
			ExitUnavailable,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	var envelope json.RawMessage
	var raw struct {
		Data json.RawMessage `json:"data"`
		Meta map[string]any  `json:"meta"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Data) == 0 {
		return nil, invalidAPIResponse()
	}
	envelope = raw.Data
	if err := json.Unmarshal(envelope, destination); err != nil {
		return nil, invalidAPIResponse()
	}
	return raw.Meta, nil
}

func readAPIToken(target APITransport) (string, error) {
	if target.TokenFile() != "" {
		file, err := os.Open(target.TokenFile())
		if err != nil {
			return "", NewCommandError(
				"TOKEN_FILE_UNREADABLE",
				"the owner token file could not be read",
				"check --token-file and its permissions",
				ExitIO,
			).WithCause(err)
		}
		defer func() { _ = file.Close() }()
		contents, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes+1))
		if err != nil {
			return "", NewCommandError("TOKEN_FILE_UNREADABLE", "the owner token file could not be read", "check --token-file and its permissions", ExitIO).WithCause(err)
		}
		if len(contents) > maxTokenFileBytes {
			return "", invalidArgument("--token-file is too large", "provide a file containing only the owner token")
		}
		return validateAPIToken(string(contents), "--token-file")
	}
	return validateAPIToken(os.Getenv("MOMENT_TOKEN"), "MOMENT_TOKEN")
}

func validateAPIToken(value, source string) (string, error) {
	token := strings.TrimSpace(value)
	if token == "" {
		return "", NewCommandError(
			"OWNER_TOKEN_REQUIRED",
			"an owner API token is required",
			"set MOMENT_TOKEN or provide --token-file",
			ExitUnauthorized,
		)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", invalidArgument(source+" must contain exactly one token", "remove surrounding content and keep only the owner token")
	}
	return token, nil
}

func decodeOwnerAPIFailure(status int, body []byte) error {
	var envelope apiFailure
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code == "" {
		return invalidAPIResponse()
	}
	exit := ExitUnavailable
	suggestion := "inspect the running Moment server and retry"
	switch envelope.Error.Code {
	case "UNAUTHORIZED", "CSRF_INVALID":
		exit = ExitUnauthorized
		suggestion = "update MOMENT_TOKEN or --token-file with the current owner token"
	case "INSTANCE_IN_USE", "OWNER_ALREADY_INITIALIZED", "OWNER_NOT_INITIALIZED":
		exit = ExitConflict
	case "INVALID_ARGUMENT":
		exit = ExitInvalidInput
	case "POST_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "run 'moment post list --json' to inspect available posts"
	case "COMMENT_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "run 'moment comment list <post-id> --json' to inspect available comments"
	case "CONTENT_EMPTY", "CONTENT_TOO_LARGE", "EXTERNAL_IMAGE_NOT_ALLOWED", "IDEMPOTENCY_KEY_REUSED":
		exit = ExitConflict
		suggestion = "correct the content or use a new idempotency key, then retry"
	case "MEDIA_NOT_FOUND":
		exit = ExitNotFound
		suggestion = "choose an existing managed media ID or clear the reference"
	case "DATA_INTEGRITY_FAILED", "EXPORT_INVALID", "EXPORT_CHECKSUM_MISMATCH":
		exit = ExitIntegrity
		suggestion = "run diagnostics before retrying"
	case "CANCELLED":
		exit = ExitTimeout
		suggestion = "retry when the caller can keep the operation context active"
	case "INTERNAL":
		exit = ExitInternal
	default:
		if status >= 400 && status < 500 {
			exit = ExitConflict
		}
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = "the Moment API rejected the owner request"
	}
	return NewCommandError(envelope.Error.Code, message, suggestion, exit).WithDetails(envelope.Error.Details)
}

func invalidAPIResponse() error {
	return NewCommandError(
		"DEPENDENCY_INVALID_RESPONSE",
		"the Moment API returned an invalid response",
		"check that --url points to a compatible Moment server",
		ExitUnavailable,
	)
}

func ownerAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInstanceInUse):
		return NewCommandError(
			"INSTANCE_IN_USE",
			"the Moment server is using this data directory",
			"stop the server or use API mode for token rotation",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return NewCommandError(
			"OWNER_ALREADY_INITIALIZED",
			"owner authentication is already initialized",
			"run 'moment owner status --json' or rotate the token instead",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrNotInitialized):
		return NewCommandError(
			"OWNER_NOT_INITIALIZED",
			"owner authentication is not initialized",
			"run 'moment owner init --data-dir <path> --json' while the server is stopped",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrSecretPermissions), errors.Is(err, auth.ErrInvalidSecrets):
		return NewCommandError(
			"OWNER_SECRETS_INVALID",
			"owner authentication secrets are invalid or insecure",
			"restore a protected secrets.json or initialize a new empty instance",
			ExitIntegrity,
		).WithCause(err)
	default:
		return err
	}
}

func ownerRuntimeError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return NewCommandError(
		"INSTANCE_NOT_READY",
		"the Moment data directory is not initialized and ready",
		"run 'moment owner init --data-dir <path> --json' for an empty instance or inspect the existing directory",
		ExitIO,
	).WithCause(err)
}
