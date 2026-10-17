package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/auth"
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
		return "", invalidArgument("--password-file is too large", "provide only the account password")
	}
	contents = bytes.TrimSuffix(contents, []byte("\r\n"))
	contents = bytes.TrimSuffix(contents, []byte("\n"))
	return string(contents), nil
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

func authenticatedAPIRawRequest(ctx context.Context, invocation Invocation, method, endpoint string, strictJSON bool) (json.RawMessage, map[string]any, int, error) {
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
		return nil, nil, response.StatusCode, decodeAPIFailure(response.StatusCode, body)
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

// apiToken resolves the configured account token. When required is false an
// unconfigured token is simply absent, while a configured one that cannot be
// read is still an error: silently reading as a stranger because a file was
// unreadable would hide exactly the misconfiguration the caller needs to see.
func apiToken(target APITransport, required bool) (string, error) {
	if required || target.TokenFile() != "" || strings.TrimSpace(os.Getenv("MOMENT_TOKEN")) != "" {
		return readAPIToken(target)
	}
	return "", nil
}

func readAPIToken(target APITransport) (string, error) {
	if target.TokenFile() != "" {
		file, err := os.Open(target.TokenFile())
		if err != nil {
			return "", NewCommandError(
				"TOKEN_FILE_UNREADABLE",
				"the account token file could not be read",
				"check --token-file and its permissions",
				ExitIO,
			).WithCause(err)
		}
		defer func() { _ = file.Close() }()
		contents, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes+1))
		if err != nil {
			return "", NewCommandError("TOKEN_FILE_UNREADABLE", "the account token file could not be read", "check --token-file and its permissions", ExitIO).WithCause(err)
		}
		if len(contents) > maxTokenFileBytes {
			return "", invalidArgument("--token-file is too large", "provide a file containing only the account token")
		}
		return validateAPIToken(string(contents), "--token-file")
	}
	return validateAPIToken(os.Getenv("MOMENT_TOKEN"), "MOMENT_TOKEN")
}

func validateAPIToken(value, source string) (string, error) {
	token := strings.TrimSpace(value)
	if token == "" {
		return "", NewCommandError(
			"ACCOUNT_TOKEN_REQUIRED",
			"an account API token is required",
			"set MOMENT_TOKEN or provide --token-file",
			ExitUnauthorized,
		)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", invalidArgument(source+" must contain exactly one token", "remove surrounding content and keep only the account token")
	}
	return token, nil
}

func decodeAPIFailure(status int, body []byte) error {
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

func authenticationError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInstanceInUse):
		return NewCommandError(
			"INSTANCE_IN_USE",
			"the Moment server is using this data directory",
			"stop the server or use a supported account API operation",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return NewCommandError(
			"SIGNING_ALREADY_INITIALIZED",
			"session signing is already initialized",
			"select an existing account with 'moment auth status --username <name> --json'",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrNotInitialized):
		return NewCommandError(
			"SIGNING_NOT_INITIALIZED",
			"session signing is not initialized",
			"run 'moment init --username <name> --data-dir <path> --json' while the server is stopped",
			ExitConflict,
		).WithCause(err)
	case errors.Is(err, auth.ErrSecretPermissions), errors.Is(err, auth.ErrInvalidSecrets):
		return NewCommandError(
			"SIGNING_MATERIAL_INVALID",
			"session signing secrets are invalid or insecure",
			"restore a protected secrets.json or initialize a new empty instance",
			ExitIntegrity,
		).WithCause(err)
	default:
		return err
	}
}

func runtimeError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return NewCommandError(
		"INSTANCE_NOT_READY",
		"the Moment data directory is not initialized and ready",
		"run 'moment init --username <name> --data-dir <path> --json' for an empty instance or inspect the existing directory",
		ExitIO,
	).WithCause(err)
}
