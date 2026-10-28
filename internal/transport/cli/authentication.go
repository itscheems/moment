package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type accountSecurityStatus struct {
	Methods         []domain.CredentialKind `json:"methods"`
	PasswordEnabled bool                    `json:"password_enabled"`
}

func registerAuthentication(application *Application) {
	username := func(flags *pflag.FlagSet) {
		flags.String("username", "", "account username for local operations; not accepted in API mode")
	}
	passwordFlags := func(flags *pflag.FlagSet) {
		username(flags)
		flags.Var(&onceStringFlag{name: "--password-file"}, "password-file", "read the password from one private file or - for stdin")
	}
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"init"}, Short: "Initialize a named administrator account",
		Long:       "Initialize a stopped instance, name its first account, and issue a normal account API token.",
		Inputs:     "Required --username. Optional --password-file. No secret is accepted as an argument.",
		Output:     "Account credential metadata and the newly issued API token, shown once.",
		Errors:     "An existing signing key is refused; a running server returns INSTANCE_IN_USE.",
		Safety:     "Offline only; initialization holds the instance import and serve locks.",
		Example:    "moment init --data-dir ./moment-data --username writer --json",
		Capability: CapabilityDirectOnly, Flags: passwordFlags, Run: runAuthenticationInit,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "status"}, Short: "Inspect an account's local sign-in methods",
		Long:       "Inspect configured password and API token methods for one account; roles do not select credentials.",
		Inputs:     "Direct mode requires --username and a stopped server. API mode uses the authenticated account.",
		Output:     "Configured method names and password_enabled, without credential material.",
		Errors:     "Unknown accounts and unavailable account storage are refused.",
		Example:    "moment auth status --data-dir ./moment-data --username writer --json",
		Capability: CapabilityBoth, Flags: username, Run: runAuthenticationStatus,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "register"}, Short: "Register a member account",
		Long:       "Create a password-backed member account under the site's open, approval, or closed registration policy. API mode sends an anonymous request; direct mode requires a stopped instance.",
		Inputs:     "Required --username and --password-file path or -. The file or stdin contains only the password and may end in one line break.",
		Output:     "Whether the account was created, its normalized username, and active or pending status. Browser session credentials are never printed.",
		Errors:     "Invalid or occupied names, invalid passwords, and closed registration return stable codes. An uncertain API result asks you to try signing in before another registration attempt.",
		Safety:     "No password is accepted in argv. Direct mode holds offline instance locks; API mode ignores ambient account tokens.",
		Example:    "moment auth register --data-dir ./moment-data --username newmember --password-file ./password.txt --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("username", "", "username for the new member account")
			flags.Var(&onceStringFlag{name: "--password-file"}, "password-file", "read the password from one private file or - for stdin")
		},
		Run: runAccountRegister,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "set-password"}, Short: "Set one account's sign-in password",
		Long:       "Replace an account password and revoke all of its browser sessions and API tokens.",
		Inputs:     "--password-file path or -. Direct mode requires --username and a stopped server; API mode acts only on itself.",
		Output:     "Password configuration and reauthentication result; no password is returned.",
		Errors:     "Invalid password input is refused without replacing the stored credential.",
		Safety:     "Passwords are read from a bounded file or stdin, never from argv or logs.",
		Example:    "moment auth set-password --data-dir ./moment-data --username writer --password-file ./password.txt --json",
		Capability: CapabilityBoth, Flags: passwordFlags, Run: runAccountPasswordSet,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "create-token"}, Short: "Create an account API token",
		Long:       "Create an independently revocable API token bound to a real, active account.",
		Inputs:     "Optional --label. Direct mode requires --username and a stopped server; API mode acts only on itself.",
		Output:     "Credential metadata and a plaintext token, returned only at creation.",
		Errors:     "Pending or suspended accounts cannot issue API tokens.",
		Example:    "moment auth create-token --data-dir ./moment-data --username writer --label laptop --json",
		Capability: CapabilityBoth, Flags: func(flags *pflag.FlagSet) { username(flags); flags.String("label", "", "device label") }, Run: runAccountTokenCreate,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "list-tokens"}, Short: "List account API token metadata",
		Long:       "List the selected account's API credentials without plaintext values or digests.",
		Inputs:     "Direct mode requires --username and a stopped server; API mode acts only on itself.",
		Output:     "Token IDs, labels, creation times and revocation metadata.",
		Errors:     "Unresolvable accounts and unavailable storage are refused.",
		Example:    "moment auth list-tokens --data-dir ./moment-data --username writer --json",
		Capability: CapabilityBoth, Flags: username, Run: runAccountTokenList,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"auth", "revoke-token"}, ArgsUsage: "<token-id>", Short: "Revoke one account API token",
		Long:    "Revoke a selected API credential while leaving other account credentials unchanged.",
		Inputs:  "One token ID. Direct mode requires --username; API mode can revoke only its own account's token.",
		Output:  "Revocation result; dry-run previews the selected credential without changing it.",
		Errors:  "Missing credentials and another account's credentials are reported as not found.",
		Safety:  "Revocation requires confirmation. --dry-run never changes credentials.",
		Example: "moment auth revoke-token <token-id> --data-dir ./moment-data --username writer --yes --json",
		Args:    cobra.ExactArgs(1), Capability: CapabilityBoth, Flags: username,
		Policy: SafetyPolicy{Destructive: true, SupportsDryRun: true, Confirmation: "Revoke this API credential?"}, Run: runAccountTokenRevoke,
	})
}

func runAuthenticationInit(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	username, err := input.String("username")
	if err != nil || strings.TrimSpace(username) == "" {
		return Result{}, invalidArgument("--username is required", "choose a username for the initial account")
	}
	username, err = domain.NormalizeUsername(username)
	if err != nil {
		return Result{}, accountCLIError(err)
	}
	password := ""
	if input.Changed("password-file") {
		path, _ := input.String("password-file")
		password, err = readPasswordInput(input.In(), path)
		if err != nil {
			return Result{}, err
		}
	}
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	state, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: target.DataDirectory()})
	if err != nil {
		return Result{}, runtimeError(err)
	}
	var issued app.IssuedAPIToken
	err = auth.WithOfflineInitializationLocks(ctx, state, func(held *runtimefs.LeaseSet) (resultErr error) {
		status, err := auth.ReadStatus(state.Root)
		if err != nil {
			return err
		}
		if status.Initialized {
			return auth.ErrAlreadyInitialized
		}
		if err := state.RefreshCurrent(held); err != nil {
			return err
		}
		database, err := sqlitestore.Open(ctx, state, sqlitestore.Options{})
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
		repository, err := sqlitestore.NewAccountRepository(database, state.Locks)
		if err != nil {
			return err
		}
		accounts, err := app.NewAccountService(repository)
		if err != nil {
			return err
		}
		account, err := accounts.InitializeSiteAccount(ctx, username)
		if err != nil {
			return err
		}
		if input.Changed("password-file") {
			if err := accounts.SetAccountPassword(ctx, account.ID, password); err != nil {
				return err
			}
		}
		issued, err = accounts.CreateAPIToken(ctx, account.ID, "initial setup")
		if err != nil {
			return err
		}
		_, err = auth.InitializeOfflineWithinLocks(ctx, state, held, auth.Options{})
		return err
	})
	if err != nil {
		return Result{}, authenticationError(accountCLIError(err))
	}
	return credentialResult("Account initialized", issued), nil
}

func withOfflineAccount(ctx context.Context, invocation Invocation, input CommandInput, operation func(*app.AccountService, domain.User) (Result, error)) (Result, error) {
	name, err := input.String("username")
	if err != nil || strings.TrimSpace(name) == "" {
		return Result{}, invalidArgument("--username is required in direct mode", "select the account explicitly")
	}
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	state, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return Result{}, runtimeError(err)
	}
	var result Result
	err = auth.WithOfflineInitializationLocks(ctx, state, func(held *runtimefs.LeaseSet) (resultErr error) {
		if err := state.RefreshCurrent(held); err != nil {
			return err
		}
		database, err := sqlitestore.Open(ctx, state, sqlitestore.Options{})
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
		repository, err := sqlitestore.NewAccountRepository(database, state.Locks)
		if err != nil {
			return err
		}
		accounts, err := app.NewAccountService(repository)
		if err != nil {
			return err
		}
		account, err := accounts.AccountByUsername(ctx, name)
		if err != nil {
			return err
		}
		result, err = operation(accounts, account)
		return err
	})
	return result, authenticationError(accountCLIError(err))
}

func rejectAPIUsername(input CommandInput) error {
	if input.Changed("username") {
		return invalidArgument("API mode uses the authenticated account", "omit --username; use credentials belonging to the intended account")
	}
	return nil
}

func runAuthenticationStatus(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	if invocation.Mode() == ModeDirect {
		return withOfflineAccount(ctx, invocation, input, func(accounts *app.AccountService, user domain.User) (Result, error) {
			methods, err := accounts.AccountSignInMethods(ctx, user.ID)
			if err != nil {
				return Result{}, err
			}
			return Result{Data: accountSecurityStatus{Methods: methods, PasswordEnabled: domain.CredentialKindsContain(methods, domain.CredentialPassword)}, Text: "Account sign-in methods inspected"}, nil
		})
	}
	if err := rejectAPIUsername(input); err != nil {
		return Result{}, err
	}
	var status accountSecurityStatus
	meta, err := accountAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/account/security", nil, http.StatusOK, &status)
	return Result{Data: status, Meta: meta, Text: "Account sign-in methods inspected"}, err
}

func runAccountPasswordSet(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	path, err := input.String("password-file")
	if err != nil || !input.Changed("password-file") {
		return Result{}, invalidArgument("--password-file is required", "provide a private file or - for stdin")
	}
	password, err := readPasswordInput(input.In(), path)
	if err != nil {
		return Result{}, err
	}
	result := Result{Data: map[string]bool{"password_enabled": true, "reauthentication_required": true}, Text: "Password updated; browser sessions and API tokens revoked"}
	if invocation.Mode() == ModeDirect {
		return withOfflineAccount(ctx, invocation, input, func(accounts *app.AccountService, user domain.User) (Result, error) {
			return result, accounts.SetAccountPassword(ctx, user.ID, password)
		})
	}
	if err := rejectAPIUsername(input); err != nil {
		return Result{}, err
	}
	var response map[string]bool
	meta, err := accountAPIRequest(ctx, invocation, http.MethodPut, "/api/v1/account/password", map[string]string{"password": password}, http.StatusOK, &response)
	result.Data = response
	result.Meta = meta
	return result, err
}

func credentialResult(message string, issued app.IssuedAPIToken) Result {
	return Result{Data: issued, Text: fmt.Sprintf("%s.\nAPI token: %s\nSave this account token now; it will not be shown again.", message, issued.Value)}
}

func runAccountTokenCreate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	label, _ := input.String("label")
	if invocation.Mode() == ModeDirect {
		return withOfflineAccount(ctx, invocation, input, func(accounts *app.AccountService, user domain.User) (Result, error) {
			issued, err := accounts.CreateAPIToken(ctx, user.ID, label)
			return credentialResult("API credential created", issued), err
		})
	}
	if err := rejectAPIUsername(input); err != nil {
		return Result{}, err
	}
	var issued app.IssuedAPIToken
	meta, err := accountAPIRequest(ctx, invocation, http.MethodPost, "/api/v1/account/tokens", map[string]string{"label": label}, http.StatusCreated, &issued)
	if err != nil {
		return Result{}, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(issued.Value)
	if err != nil || len(decoded) != 32 || domain.ValidateUserID(issued.Token.ID) != nil || domain.ValidateUserID(issued.Token.UserID) != nil || issued.Token.CreatedAtMS <= 0 || !issued.Token.Live() {
		return Result{}, invalidAPIResponse()
	}
	result := credentialResult("API credential created", issued)
	result.Meta = meta
	return result, nil
}

func runAccountTokenList(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	if invocation.Mode() == ModeDirect {
		return withOfflineAccount(ctx, invocation, input, func(accounts *app.AccountService, user domain.User) (Result, error) {
			tokens, err := accounts.APITokens(ctx, user.ID)
			return Result{Data: tokens, Text: fmt.Sprintf("%d account API credential(s)", len(tokens))}, err
		})
	}
	if err := rejectAPIUsername(input); err != nil {
		return Result{}, err
	}
	var tokens []domain.APIToken
	meta, err := accountAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/account/tokens", nil, http.StatusOK, &tokens)
	return Result{Data: tokens, Meta: meta, Text: fmt.Sprintf("%d account API credential(s)", len(tokens))}, err
}

func runAccountTokenRevoke(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	id := input.Args()[0]
	preview := func(tokens []domain.APIToken) (Result, error) {
		for _, token := range tokens {
			if token.ID == id {
				return Result{Data: map[string]any{"id": id, "revoked": false, "dry_run": true}, Text: "API credential revocation preview"}, nil
			}
		}
		return Result{}, canonicalPublicError(string(domain.CodeAPITokenNotFound), "API credential does not exist for this account", nil, http.StatusNotFound)
	}
	if invocation.Mode() == ModeDirect {
		return withOfflineAccount(ctx, invocation, input, func(accounts *app.AccountService, user domain.User) (Result, error) {
			if invocation.Safety().DryRun {
				tokens, err := accounts.APITokens(ctx, user.ID)
				if err != nil {
					return Result{}, err
				}
				return preview(tokens)
			}
			return Result{Data: map[string]bool{"revoked": true}, Text: "API credential revoked"}, accounts.RevokeAPIToken(ctx, user.ID, id)
		})
	}
	if err := rejectAPIUsername(input); err != nil {
		return Result{}, err
	}
	if invocation.Safety().DryRun {
		var tokens []domain.APIToken
		if _, err := accountAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/account/tokens", nil, http.StatusOK, &tokens); err != nil {
			return Result{}, err
		}
		return preview(tokens)
	}
	var response map[string]bool
	meta, err := accountAPIRequest(ctx, invocation, http.MethodDelete, "/api/v1/account/tokens/"+url.PathEscape(id), nil, http.StatusOK, &response)
	return Result{Data: response, Meta: meta, Text: "API credential revoked"}, err
}

func accountAPIRequest(ctx context.Context, invocation Invocation, method, path string, payload any, expectedStatus int, destination any) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), path)
	if err != nil {
		return nil, err
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode != expectedStatus {
		return nil, decodeAPIFailure(response.StatusCode, encoded)
	}
	_, meta, err := decodeStrictAPISuccess(encoded, destination)
	return meta, err
}
