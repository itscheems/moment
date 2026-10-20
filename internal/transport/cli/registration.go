package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type registrationResult struct {
	AccountCreated bool                 `json:"account_created"`
	Username       string               `json:"username"`
	AccountStatus  domain.AccountStatus `json:"account_status"`
}

type registrationAPIReceipt struct {
	AccountCreated bool                 `json:"account_created"`
	Username       string               `json:"username"`
	AccountStatus  domain.AccountStatus `json:"account_status"`
	ExpiresAt      time.Time            `json:"expires_at"`
	CSRFToken      string               `json:"csrf_token"`
}

func runAccountRegister(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	username, err := input.String("username")
	if err != nil || strings.TrimSpace(username) == "" {
		return Result{}, invalidArgument("--username is required", "choose a username for the new account")
	}
	path, err := input.String("password-file")
	if err != nil || !input.Changed("password-file") {
		return Result{}, invalidArgument("--password-file is required", "provide a private file or - for stdin")
	}
	if strings.TrimSpace(invocation.Options().As) != "" {
		return Result{}, invalidArgument("--as does not apply to registration", "omit --as; the new account is always a member")
	}
	if invocation.Mode() == ModeAPI && invocation.Options().TokenFile != "" {
		return Result{}, invalidArgument("registration is anonymous", "omit --token-file; the command does not use an account token")
	}
	password, err := readPasswordInput(input.In(), path)
	if err != nil {
		return Result{}, err
	}
	var result registrationResult
	var meta map[string]any
	if invocation.Mode() == ModeDirect {
		result, err = registerDirect(ctx, invocation, username, password)
	} else {
		result, meta, err = registerAPI(ctx, invocation, username, password)
	}
	if err != nil {
		return Result{}, err
	}
	message := fmt.Sprintf("Account @%s created", result.Username)
	if result.AccountStatus == domain.AccountPending {
		message += "; waiting for administrator approval"
	}
	return Result{Data: result, Meta: meta, Text: message}, nil
}

func registerDirect(ctx context.Context, invocation Invocation, username, password string) (registrationResult, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return registrationResult{}, err
	}
	state, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return registrationResult{}, runtimeError(err)
	}
	var created domain.User
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
		created, err = accounts.RegisterPasswordAccount(ctx, username, password)
		return err
	})
	if err != nil {
		if created.ID != "" {
			return registrationResult{}, NewCommandError("REGISTRATION_COMPLETED_WITH_CLEANUP_ERROR", "the account was created, but local cleanup failed", "sign in with the chosen username and password; do not register again", ExitIO).WithDetails(map[string]any{"account_created": true, "username": created.Username}).WithCause(err)
		}
		return registrationResult{}, authenticationError(accountCLIError(err))
	}
	return registrationResult{AccountCreated: true, Username: created.Username, AccountStatus: created.Status}, nil
}

func registerAPI(ctx context.Context, invocation Invocation, username, password string) (registrationResult, map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return registrationResult{}, nil, err
	}
	request, err := newAPIRequest(ctx, target, http.MethodPost, "/api/v1/register", map[string]string{"username": username, "password": password})
	if err != nil {
		return registrationResult{}, nil, err
	}
	// Registration must stay anonymous even when MOMENT_TOKEN is configured.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return registrationResult{}, nil, uncertainRegistration(err, ctx)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return registrationResult{}, nil, uncertainRegistration(err, ctx)
	}
	if len(body) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) {
		return registrationResult{}, nil, uncertainRegistration(invalidAPIResponse(), ctx)
	}
	if response.StatusCode != http.StatusCreated {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failure := decodeAPIFailure(response.StatusCode, body)
			var public *CommandError
			if errors.As(failure, &public) && public.Code != "DEPENDENCY_INVALID_RESPONSE" {
				return registrationResult{}, nil, failure
			}
		}
		return registrationResult{}, nil, uncertainRegistration(invalidAPIResponse(), ctx)
	}
	var receipt registrationAPIReceipt
	_, meta, err := decodeStrictAPISuccess(body, &receipt)
	if err != nil || !receipt.AccountCreated || receipt.Username == "" || receipt.CSRFToken == "" || receipt.ExpiresAt.IsZero() ||
		(receipt.AccountStatus != domain.AccountActive && receipt.AccountStatus != domain.AccountPending) {
		return registrationResult{}, nil, uncertainRegistration(invalidAPIResponse(), ctx)
	}
	canonical, err := domain.NormalizeUsername(username)
	if err != nil || canonical != receipt.Username {
		return registrationResult{}, nil, uncertainRegistration(invalidAPIResponse(), ctx)
	}
	return registrationResult{AccountCreated: true, Username: receipt.Username, AccountStatus: receipt.AccountStatus}, meta, nil
}

func uncertainRegistration(cause error, ctx context.Context) error {
	exit := ExitUnavailable
	if ctx.Err() != nil {
		exit = ExitTimeout
	}
	return NewCommandError("REGISTRATION_OUTCOME_UNKNOWN", "the server response did not confirm whether the account was created", "try signing in with the chosen username and password before another registration attempt", exit).WithCause(cause)
}
