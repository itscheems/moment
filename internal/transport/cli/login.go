package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/observability"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// defaultSessionFile follows the owner.token convention: a private file
	// under the ignored local directory, beside the credential this one is the
	// member-account counterpart of.
	//
	// It is JSON rather than a bare token because a session is three things a
	// caller needs together -- the signed value, the CSRF token every unsafe
	// request must echo, and the instance it was issued by. Re-fetching the
	// CSRF token before each write would cost a round trip for material that is
	// already bound to the same credential and worth exactly as much as it.
	defaultSessionFile = ".moment-local/session.json"
	// sessionCookieName is what the server reads a session out of. It is spelt
	// here rather than imported because the CLI must not depend on the HTTP
	// transport package; the name is part of the published wire contract.
	sessionCookieName = "moment_session"
	// csrfHeaderName is the same, for the token an unsafe request must echo.
	csrfHeaderName = "X-CSRF-Token"
	// maxSessionFileBytes bounds what will be read back as a stored session.
	maxSessionFileBytes = 8 << 10
	// deviceSlowDownStep is what a slow_down adds when the server names no new
	// interval of its own. It is the step the device flow specifies, and using
	// it unconditionally as a floor keeps the interval strictly increasing even
	// if a provider echoes the interval it already gave us.
	deviceSlowDownStep = 5 * time.Second
	// deviceMinInterval and deviceMaxWait bound the loop. The provider's expiry
	// normally ends it; the ceiling exists so a server that reports none cannot
	// leave a terminal polling forever.
	deviceMinInterval = time.Second
	deviceMaxWait     = 15 * time.Minute
)

func registerLogin(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"login"},
		ArgsUsage: "<provider>",
		Short:     "Sign in to a Moment server from this terminal",
		Long: "Sign in through an identity provider using the OAuth device flow, which needs no browser " +
			"redirect back to this machine, and keep the session the server issues.",
		Inputs: "One provider name such as github, plus --url naming the Moment server. " +
			"--session-file chooses where the issued session is written.",
		Output: "The code to enter and the address to enter it at are written to stderr while waiting; " +
			"the account that signed in and the file the session was written to are the result.",
		Errors: "A code nobody approved in time returns OAUTH_DEVICE_EXPIRED and a refused one returns " +
			"OAUTH_DEVICE_DENIED. A site not accepting new accounts returns REGISTRATION_CLOSED; " +
			"a provider with no device flow returns OAUTH_PROVIDER_UNAVAILABLE.",
		Example:     "moment --url http://127.0.0.1:8080 login github",
		Args:        cobra.ExactArgs(1),
		Capability:  CapabilityAPIOnly,
		LongRunning: true,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("session-file", defaultSessionFile, "file the issued session credential is written to")
		},
		Run: runLogin,
	})
}

// deviceStart is the first leg's answer: what to show a person, and what to
// send back on every poll.
type deviceStart struct {
	DeviceCode       string `json:"device_code"`
	UserCode         string `json:"user_code"`
	VerificationURI  string `json:"verification_uri"`
	IntervalSeconds  int    `json:"interval_seconds"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

// devicePoll is one poll's answer. Session is a pointer and not a string on
// purpose: a poll that is still waiting carries no session at all, so reading
// one without checking status finds nothing rather than an empty credential.
type devicePoll struct {
	Status          string         `json:"status"`
	IntervalSeconds int            `json:"interval_seconds"`
	Session         *deviceSession `json:"session"`
}

type deviceSession struct {
	Value     string    `json:"value"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// storedSession is what a successful login leaves on disk.
//
// The instance is recorded beside the credential because a session is only
// meaningful to the server that signed it, and a file that does not say which
// one invites presenting it to another.
type storedSession struct {
	BaseURL   string    `json:"base_url"`
	Session   string    `json:"session"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loginResult is the machine answer. It names the account and where the
// credential went, and never the credential: this document reaches stdout, and
// a session value on stdout is one shell history away from being someone
// else's.
type loginResult struct {
	Provider    string    `json:"provider"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	ProfileURL  string    `json:"profile_url"`
	SessionFile string    `json:"session_file"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func runLogin(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	provider, err := loginProviderArgument(input)
	if err != nil {
		return Result{}, err
	}
	sessionFile, err := input.String("session-file")
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(sessionFile) == "" {
		return Result{}, invalidArgument("--session-file cannot be empty", "provide a path, or omit the flag for "+defaultSessionFile)
	}
	target, err := invocation.APITransport()
	if err != nil {
		return Result{}, err
	}

	var started deviceStart
	if err := deviceAPIRequest(ctx, target, "/api/v1/login/device/"+provider+"/start", nil, &started); err != nil {
		return Result{}, err
	}
	if started.DeviceCode == "" || started.UserCode == "" || started.VerificationURI == "" {
		return Result{}, invalidAPIResponse()
	}

	// stderr, not stdout: stdout carries one machine document and nothing else,
	// and a person still has to be able to read this while --json is on.
	invocation.Log("info", fmt.Sprintf("open %s and enter the code %s", started.VerificationURI, started.UserCode))
	invocation.Log("info", "waiting for the code to be approved; press Ctrl-C to stop")

	session, err := awaitDeviceApproval(ctx, invocation, target, provider, started)
	if err != nil {
		return Result{}, err
	}
	credential := storedSession{
		BaseURL:   target.BaseURL().String(),
		Session:   session.Value,
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt.UTC(),
	}
	if err := writeStoredSession(sessionFile, credential); err != nil {
		return Result{}, err
	}
	// The credential is read back off disk and then used, rather than used from
	// memory. That is the round trip every later command makes, and doing it
	// here is what turns "the file was written" into "the file works": a
	// credential nothing can load and present is worse than none at all,
	// because it looks like being signed in.
	stored, err := readStoredSession(sessionFile)
	if err != nil {
		return Result{}, err
	}
	account, err := readSignedInAccount(ctx, target, stored)
	if err != nil {
		return Result{}, err
	}
	text := fmt.Sprintf("Signed in as @%s (%s)\nSession stored in %s\nExpires %s",
		account.Username, account.Status, sessionFile, credential.ExpiresAt.Format(time.RFC3339))
	if account.Status == "pending" {
		text += "\nThis account is waiting for the site owner to approve it before it may post."
	}
	return Result{
		Data: loginResult{
			Provider:    provider,
			Username:    account.Username,
			DisplayName: account.DisplayName,
			Status:      account.Status,
			ProfileURL:  account.ProfileURL,
			SessionFile: sessionFile,
			ExpiresAt:   credential.ExpiresAt,
		},
		Text: text,
	}, nil
}

// awaitDeviceApproval polls until somebody approves the code, refuses it, or
// lets it expire.
func awaitDeviceApproval(
	ctx context.Context,
	invocation Invocation,
	target APITransport,
	provider string,
	started deviceStart,
) (deviceSession, error) {
	interval := max(time.Duration(started.IntervalSeconds)*time.Second, deviceMinInterval)
	wait := time.Duration(started.ExpiresInSeconds) * time.Second
	if wait <= 0 || wait > deviceMaxWait {
		wait = deviceMaxWait
	}
	deadline := time.Now().Add(wait)
	body := map[string]string{"device_code": started.DeviceCode}

	for {
		if err := sleepUntilNextPoll(ctx, interval); err != nil {
			return deviceSession{}, err
		}
		// Stopping locally rather than polling a code we know is dead. The
		// server reports the expiry too, but only if it is still answering.
		if time.Now().After(deadline) {
			return deviceSession{}, deviceExpiredError()
		}
		var answer devicePoll
		if err := deviceAPIRequest(ctx, target, "/api/v1/login/device/"+provider+"/poll", body, &answer); err != nil {
			return deviceSession{}, err
		}
		switch answer.Status {
		case "authorized":
			if answer.Session == nil || answer.Session.Value == "" {
				return deviceSession{}, invalidAPIResponse()
			}
			return *answer.Session, nil
		case "pending":
		case "slow_down":
			interval = nextPollInterval(interval, answer.IntervalSeconds)
			invocation.Log("debug", fmt.Sprintf("the server asked for a slower poll; waiting %s", interval))
		default:
			return deviceSession{}, invalidAPIResponse()
		}
	}
}

// nextPollInterval is what to wait after being told to slow down.
//
// Strictly longer than what we were already doing, whatever was reported. The
// provider names a new interval and normally it is larger, but a caller that
// took a repeated or missing number at face value would keep polling at the
// rate it was just told off for -- which is how a deployment gets refused the
// endpoint altogether rather than merely asked twice.
func nextPollInterval(current time.Duration, reportedSeconds int) time.Duration {
	return max(time.Duration(reportedSeconds)*time.Second, current+deviceSlowDownStep)
}

// sleepUntilNextPoll waits out the interval without ignoring a cancelled
// command. A plain sleep would make Ctrl-C take up to a full interval to land.
func sleepUntilNextPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func deviceExpiredError() error {
	return NewCommandError(
		"OAUTH_DEVICE_EXPIRED",
		"the device code expired before it was approved",
		"run the login command again to request a new code",
		ExitTimeout,
	)
}

func loginProviderArgument(input CommandInput) (string, error) {
	args := input.Args()
	if len(args) != 1 {
		return "", invalidArgument("one provider name is required", "pass exactly one provider, such as github")
	}
	provider := strings.ToLower(strings.TrimSpace(args[0]))
	// The name goes into a path segment. Anything that is not a bare provider
	// key would either address a different route or be rejected by the server
	// after a pointless round trip.
	if provider == "" || strings.Trim(provider, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return "", invalidArgument("the provider name must be a bare provider key", "use a name such as github")
	}
	return provider, nil
}

// deviceAPIRequest makes one unauthenticated call to a device login endpoint.
//
// Unauthenticated by construction: this is the one exchange whose whole purpose
// is that the caller holds no credential yet, so there is no token to read and
// nothing to fall back to when none is configured.
func deviceAPIRequest(ctx context.Context, target APITransport, endpoint string, payload any, destination any) error {
	request, err := newAPIRequest(ctx, target, http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	return sendAPIRequest(request, destination)
}

// readSignedInAccount reports who a stored session belongs to.
//
// The account shape is decoded leniently rather than with the strict decoder
// the rest of this package uses. That shape is the browser's session contract
// and grows fields for the browser's reasons; this call is a liveness check on
// a credential, and failing it because a field was added for a page this
// command never renders would be a false alarm about the wrong thing.
func readSignedInAccount(ctx context.Context, target APITransport, credential storedSession) (loginAccount, error) {
	request, err := newAPIRequest(ctx, target, http.MethodGet, "/api/v1/account/session", nil)
	if err != nil {
		return loginAccount{}, err
	}
	presentSession(request, credential)

	var envelope struct {
		Account loginAccount `json:"account"`
	}
	if err := sendAPIRequestLenient(request, &envelope); err != nil {
		return loginAccount{}, err
	}
	if envelope.Account.Username == "" {
		return loginAccount{}, invalidAPIResponse()
	}
	return envelope.Account, nil
}

type loginAccount struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	ProfileURL  string `json:"profile_url"`
}

// presentSession attaches a stored session to a request the way a browser
// would, which is the only way this server accepts one.
//
// The signed value goes in the cookie the gate already reads, unchanged, so a
// CLI session and a browser session are the same credential rather than two
// that have to agree. The CSRF token rides along on anything that writes,
// because the gate demands it of every cookie-authenticated unsafe request and
// makes no exception for a caller that is not a browser -- nor should it: the
// header is what proves the request was composed by something holding the
// session rather than by a page that merely sits next to it.
func presentSession(request *http.Request, credential storedSession) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: credential.Session})
	if unsafeAPIMethod(request.Method) {
		request.Header.Set(csrfHeaderName, credential.CSRFToken)
	}
}

func unsafeAPIMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func newAPIRequest(ctx context.Context, target APITransport, method, endpoint string, payload any) (*http.Request, error) {
	address, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(observability.SurfaceHeader, string(observability.SurfaceCLI))
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func sendAPIRequest(request *http.Request, destination any) error {
	body, err := readAPIResponse(request)
	if err != nil {
		return err
	}
	if _, _, err := decodeStrictAPISuccess(body, destination); err != nil {
		return invalidAPIResponse()
	}
	return nil
}

func sendAPIRequestLenient(request *http.Request, destination any) error {
	body, err := readAPIResponse(request)
	if err != nil {
		return err
	}
	data, _, err := decodeStrictAPIEnvelope(body)
	if err != nil {
		return invalidAPIResponse()
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return invalidAPIResponse()
	}
	return nil
}

func readAPIResponse(request *http.Request) ([]byte, error) {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	return body, nil
}

// writeStoredSession puts the credential somewhere only this account can read.
//
// Written to a fresh file and renamed into place, so the mode is the one this
// process chose rather than whatever an existing file happened to carry, and so
// a failed write never leaves half a credential behind.
func writeStoredSession(path string, credential storedSession) error {
	encoded, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return sessionFileError(err)
		}
	}
	temporary, err := os.CreateTemp(directory, ".session-*")
	if err != nil {
		return sessionFileError(err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return sessionFileError(err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return sessionFileError(err)
	}
	if err := temporary.Close(); err != nil {
		return sessionFileError(err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return sessionFileError(err)
	}
	return nil
}

// readStoredSession loads a credential a previous login left behind.
func readStoredSession(path string) (storedSession, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return storedSession{}, NewCommandError(
			"SESSION_FILE_UNREADABLE",
			"the stored session could not be read",
			"run 'moment --url <server> login <provider>' to sign in again",
			ExitUnauthorized,
		).WithCause(err)
	}
	if len(contents) > maxSessionFileBytes {
		return storedSession{}, invalidArgument("the session file is too large", "provide a file containing only one stored session")
	}
	var credential storedSession
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		strings.TrimSpace(credential.Session) == "" {
		return storedSession{}, invalidArgument("the session file does not contain a stored session",
			"run 'moment --url <server> login <provider>' to sign in again")
	}
	return credential, nil
}

func sessionFileError(cause error) error {
	return NewCommandError(
		"SESSION_FILE_UNWRITABLE",
		"the issued session could not be stored",
		"check the --session-file path and its directory permissions, then retry",
		ExitIO,
	).WithCause(cause)
}
