package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/spf13/pflag"
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
	initSpec := LeafSpec{
		Path:       []string{"owner", "init"},
		Short:      "Initialize the single owner credential",
		Long:       "Initialize the local Moment instance and create its single owner credential offline.",
		Inputs:     "A local --data-dir. No token input is accepted; a cryptographically random token is generated.",
		Output:     "The bearer token is shown exactly once with safe owner metadata. Save it immediately.",
		Errors:     "An active server returns INSTANCE_IN_USE; existing credentials return OWNER_ALREADY_INITIALIZED.",
		Safety:     "Offline only. The command owns import.lock then serve.lock exclusively through database and credential initialization, and never overwrites an existing owner.",
		Example:    "moment owner init --data-dir ./moment-data --json",
		Capability: CapabilityDirectOnly,
		Run:        runOwnerInit,
	}
	application.MustRegisterLeaf(initSpec)
	initAlias := initSpec
	initAlias.Path = []string{"init"}
	initAlias.Short = "Initialize the Moment instance and owner"
	initAlias.Long = "Initialize the local Moment instance and create its single owner credential offline. This is the exact public alias of 'moment owner init'."
	initAlias.Example = "moment init --data-dir ./moment-data --json"
	application.MustRegisterLeaf(initAlias)
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
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"owner", "set-password"},
		Short:      "Set the owner browser-login password",
		Long:       "Configure or replace the human password used only to create browser sessions. The owner bearer token remains the CLI and API credential.",
		Inputs:     "Read the password from exactly one --password-file path or - for stdin. Direct mode requires a stopped server; API mode authenticates through MOMENT_TOKEN or --token-file.",
		Output:     "Safe owner authentication status including password_enabled; password bytes are never echoed or returned as JSON.",
		Errors:     "Passwords outside 8-72 bytes return INVALID_ARGUMENT; direct mode returns INSTANCE_IN_USE for an active server.",
		Safety:     "Password material is accepted only from a bounded file or stdin, never from argv, output, logs, or exported site data.",
		Example:    "printf '%s' 'correct horse battery staple' | moment owner set-password --data-dir ./moment-data --password-file - --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.Var(&onceStringFlag{name: "--password-file"}, "password-file", "read the new password from one file path or - for stdin")
		},
		Run: runOwnerSetPassword,
	})
}

func runOwnerSetPassword(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	path, err := input.String("password-file")
	if err != nil || !input.Changed("password-file") || path == "" {
		return Result{}, invalidArgument("--password-file is required", "provide a private file path or - for stdin")
	}
	password, err := readPasswordInput(input.In(), path)
	if err != nil {
		return Result{}, err
	}
	var status auth.Status
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		runtimeState, runtimeErr := runtimefs.OpenContext(ctx, target.DataDirectory())
		if runtimeErr != nil {
			return Result{}, ownerRuntimeError(runtimeErr)
		}
		status, err = auth.SetPasswordOffline(ctx, runtimeState, password)
	case ModeAPI:
		payload, marshalErr := json.Marshal(map[string]string{"password": password})
		if marshalErr != nil {
			return Result{}, marshalErr
		}
		var meta map[string]any
		meta, err = ownerAPIJSONRequest(ctx, invocation, http.MethodPut, "/api/v1/owner/password", payload, http.StatusOK, &status)
		result := ownerStatusResult(status)
		result.Meta = meta
		if err != nil {
			return Result{}, err
		}
		return result, nil
	default:
		return Result{}, errors.New("unsupported owner password transport")
	}
	if err != nil {
		return Result{}, ownerAuthError(err)
	}
	return ownerStatusResult(status), nil
}

func readPasswordInput(stdin io.Reader, path string) (string, error) {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		reader = stdin
	} else {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", NewCommandError("PASSWORD_FILE_UNREADABLE", "the password file could not be read", "provide a readable regular file or - for stdin", ExitIO).WithCause(err)
		}
		file, err = os.Open(path)
		if err != nil {
			return "", NewCommandError("PASSWORD_FILE_UNREADABLE", "the password file could not be read", "check the path and permissions", ExitIO).WithCause(err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	contents, err := io.ReadAll(io.LimitReader(reader, maxTokenFileBytes+1))
	if err != nil {
		return "", NewCommandError("PASSWORD_FILE_UNREADABLE", "the password input could not be read", "check the selected input and retry", ExitIO).WithCause(err)
	}
	if len(contents) > maxTokenFileBytes {
		return "", invalidArgument("--password-file is too large", "provide only the owner password")
	}
	contents = bytes.TrimSuffix(contents, []byte("\r\n"))
	contents = bytes.TrimSuffix(contents, []byte("\n"))
	return string(contents), nil
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
	var credential auth.Credential
	err = auth.WithOfflineInitializationLocks(ctx, runtimeState, func(held *runtimefs.LeaseSet) error {
		if err := runtimeState.RefreshCurrent(held); err != nil {
			return err
		}
		database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
		if err != nil {
			return NewCommandError(
				"DATABASE_NOT_READY",
				"the Moment database could not be initialized",
				"inspect the selected data directory and retry",
				ExitIntegrity,
			).WithCause(err)
		}
		if err := database.Close(); err != nil {
			return err
		}
		credential, err = auth.InitializeOfflineWithinLocks(ctx, runtimeState, held, auth.Options{})
		return err
	})
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
		meta, err := ownerAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/owner/status", http.StatusOK, &status)
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
		meta, err := ownerAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/owner/token/rotate", http.StatusOK, &credential)
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

func ownerAPIRequest(ctx context.Context, invocation Invocation, method, endpoint string, expectedStatus int, destination any) (map[string]any, error) {
	data, meta, status, err := ownerAPIRawRequest(ctx, invocation, method, endpoint, true)
	if err != nil {
		return nil, err
	}
	if status != expectedStatus {
		return nil, invalidAPIResponse()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, invalidAPIResponse()
	}
	if !validOwnerAPIData(data, destination) {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func ownerAPIJSONRequest(ctx context.Context, invocation Invocation, method, endpoint string, payload []byte, expectedStatus int, destination any) (map[string]any, error) {
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
	request, err := http.NewRequestWithContext(ctx, method, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
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
		return nil, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the Moment API response exceeded the allowed size", "inspect the server and retry", ExitUnavailable)
	}
	if response.StatusCode != expectedStatus {
		return nil, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) {
		return nil, invalidAPIResponse()
	}
	data, meta, err := decodeStrictAPIEnvelope(body)
	if err != nil {
		return nil, invalidAPIResponse()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validOwnerAPIData(data, destination) {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func validOwnerAPIData(data json.RawMessage, destination any) bool {
	switch value := destination.(type) {
	case *auth.Status:
		if !hasRequiredNonNullAPIDataFields(data, "initialized", "password_enabled", "session_epoch", "initialized_at") ||
			!value.Initialized || value.SessionEpoch == 0 || !validCanonicalAPITimePointer(value.InitializedAt) ||
			!validCanonicalAPIInstantField(data, "initialized_at", *value.InitializedAt) || !validOwnerRotationState(data, value.SessionEpoch, *value.InitializedAt, value.RotatedAt) {
			return false
		}
		return true
	case *auth.Credential:
		if !hasRequiredNonNullAPIDataFields(data, "token", "initialized", "session_epoch", "initialized_at") ||
			!value.Initialized || value.SessionEpoch < 2 || len(value.Token) < 32 || strings.TrimSpace(value.Token) != value.Token ||
			strings.ContainsAny(value.Token, " \t\r\n") || !validCanonicalAPITime(value.InitializedAt) ||
			!validCanonicalAPIInstantField(data, "initialized_at", value.InitializedAt) ||
			!validOwnerRotationState(data, value.SessionEpoch, value.InitializedAt, value.RotatedAt) {
			return false
		}
		return true
	default:
		return false
	}
}

func validOwnerRotationState(data json.RawMessage, epoch uint64, initializedAt time.Time, rotatedAt *time.Time) bool {
	if epoch == 1 {
		return rotatedAt == nil && validOptionalCanonicalAPIInstantField(data, "rotated_at", nil)
	}
	return epoch > 1 && rotatedAt != nil && !rotatedAt.Before(initializedAt) &&
		validOptionalCanonicalAPIInstantField(data, "rotated_at", rotatedAt)
}

func hasRequiredAPIDataFields(data json.RawMessage, required ...string) bool {
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	for _, field := range required {
		if _, present := fields[field]; !present {
			return false
		}
	}
	return true
}

func hasRequiredNonNullAPIDataFields(data json.RawMessage, required ...string) bool {
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	for _, field := range required {
		value, present := fields[field]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

func apiDataFields(data json.RawMessage) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

func validCanonicalAPITime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Nanosecond()%int(time.Millisecond) == 0
}

func validCanonicalAPITimePointer(value *time.Time) bool {
	return value != nil && validCanonicalAPITime(*value)
}

func validCanonicalAPIInstantField(data json.RawMessage, field string, expected time.Time) bool {
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	return validCanonicalAPIRawInstant(fields[field], expected)
}

func validOptionalCanonicalAPIInstantField(data json.RawMessage, field string, expected *time.Time) bool {
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	raw, present := fields[field]
	if expected == nil {
		return !present
	}
	return present && validCanonicalAPIRawInstant(raw, *expected)
}

func validNullableCanonicalAPIInstantField(data json.RawMessage, field string, expected *time.Time) bool {
	fields, ok := apiDataFields(data)
	if !ok {
		return false
	}
	raw, present := fields[field]
	if !present {
		return false
	}
	if expected == nil {
		return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	return validCanonicalAPIRawInstant(raw, *expected)
}

func validCanonicalAPIRawInstant(raw json.RawMessage, expected time.Time) bool {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil || encoded == "" || !strings.HasSuffix(encoded, "Z") {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	return err == nil && validCanonicalAPITime(parsed) && parsed.Equal(expected) && encoded == parsed.Format(time.RFC3339Nano)
}

func ownerAPIRawRequest(ctx context.Context, invocation Invocation, method, endpoint string, strictJSON bool) (json.RawMessage, map[string]any, int, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, nil, 0, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, nil, 0, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, nil, 0, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpointURL, http.NoBody)
	if err != nil {
		return nil, nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, nil, response.StatusCode, err
	}
	if len(body) > maxAPIResponse {
		return nil, nil, response.StatusCode, NewCommandError(
			"DEPENDENCY_INVALID_RESPONSE",
			"the Moment API response exceeded the allowed size",
			"check the server and reverse proxy configuration",
			ExitUnavailable,
		)
	}
	if strictJSON && (!validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(body) != nil) {
		return nil, nil, response.StatusCode, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, response.StatusCode, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	if strictJSON {
		data, meta, decodeErr := decodeStrictAPIEnvelope(body)
		if decodeErr != nil {
			return nil, nil, response.StatusCode, invalidAPIResponse()
		}
		return data, meta, response.StatusCode, nil
	}
	var raw struct {
		Data json.RawMessage `json:"data"`
		Meta map[string]any  `json:"meta"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, response.StatusCode, invalidAPIResponse()
	}
	if len(raw.Data) == 0 || string(bytes.TrimSpace(raw.Data)) == "null" {
		return nil, nil, response.StatusCode, invalidAPIResponse()
	}
	return raw.Data, raw.Meta, response.StatusCode, nil
}

func validAPIJSONContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "application/json" || len(parameters) > 1 {
		return false
	}
	charset, hasCharset := parameters["charset"]
	return len(parameters) == 0 || (hasCharset && strings.EqualFold(charset, "utf-8"))
}

func decodeStrictAPIEnvelope(body []byte) (json.RawMessage, map[string]any, error) {
	if err := validateUniqueJSONObjects(body); err != nil {
		return nil, nil, invalidAPIResponse()
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta map[string]any  `json:"meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) || !validAPIResponseMeta(envelope.Meta) {
		return nil, nil, invalidAPIResponse()
	}
	return envelope.Data, envelope.Meta, nil
}

func validAPIResponseMeta(meta map[string]any) bool {
	requestID, ok := meta["request_id"].(string)
	return ok && strings.TrimSpace(requestID) != "" && strings.TrimSpace(requestID) == requestID
}

func decodeStrictAPISuccess(body []byte, destination any) (json.RawMessage, map[string]any, error) {
	data, meta, err := decodeStrictAPIEnvelope(body)
	if err != nil {
		return nil, nil, err
	}
	dataDecoder := json.NewDecoder(bytes.NewReader(data))
	dataDecoder.DisallowUnknownFields()
	if err := dataDecoder.Decode(destination); err != nil || dataDecoder.Decode(&struct{}{}) != io.EOF {
		return nil, nil, invalidAPIResponse()
	}
	return data, meta, nil
}

func validateUniqueJSONObjects(document []byte) error {
	if !utf8.Valid(document) {
		return errors.New("JSON document is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("JSON document is not an object")
	}
	if err := consumeUniqueJSONObject(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("JSON document contains trailing data")
		}
		return err
	}
	return nil
}

func consumeUniqueJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("JSON object key is duplicated")
		}
		seen[key] = struct{}{}
		if err := consumeUniqueJSONValue(decoder); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil || token != json.Delim('}') {
		return errors.New("JSON object is incomplete")
	}
	return nil
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return consumeUniqueJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is incomplete")
		}
		return nil
	default:
		return errors.New("JSON value has an unexpected delimiter")
	}
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
	if err := validateUniqueJSONObjects(body); err != nil {
		return invalidAPIResponse()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		!validStableErrorCode(envelope.Error.Code) ||
		strings.TrimSpace(envelope.Error.Message) == "" || envelope.Error.Details == nil ||
		strings.TrimSpace(envelope.Error.RequestID) == "" || strings.TrimSpace(envelope.Error.RequestID) != envelope.Error.RequestID {
		return invalidAPIResponse()
	}
	return canonicalPublicError(envelope.Error.Code, envelope.Error.Message, envelope.Error.Details, status)
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
	case errors.Is(err, auth.ErrInvalidPassword):
		return invalidArgument("the owner password must be 8 to 72 bytes without surrounding whitespace", "choose a longer password and retry")
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
