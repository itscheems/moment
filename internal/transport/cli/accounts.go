package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/pflag"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// accountView is the CLI projection of one account. It carries status because
// the whole reason this command group exists is deciding what a status should
// be, and a review tool that hides the field being reviewed is not one.
type accountView struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	ProfileURL  string `json:"profile_url"`
}

func accountViewOf(user domain.User) accountView {
	return accountView{
		UserID:      user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        string(user.Role),
		Status:      string(user.Status),
		ProfileURL:  domain.ProfileURL(user.Username),
	}
}

func registerAccounts(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "list"}, Short: "List the accounts on this site",
		Long:       "Report every account with its role and status, so applications waiting on approval can be seen without a browser.",
		Inputs:     "An optional --status filter of pending, active, or suspended.",
		Output:     "A JSON list of user_id, username, display_name, role, status, and profile_url.",
		Errors:     "An unrecognised status returns INVALID_ARGUMENT.",
		Example:    "moment user list --data-dir ./moment-data --status pending --json",
		Capability: CapabilityDirectOnly,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("status", "", "only accounts in this status: pending, active, or suspended")
		},
		Run: runAccountList,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "approve"}, Short: "Approve an account waiting to be admitted",
		Long:       "Move one account to active so it may write. This is the everyday operation of the approval registration mode.",
		Inputs:     "One username.",
		Output:     "The updated account.",
		Errors:     "An unknown username returns ACCOUNT_NOT_FOUND; the owner account cannot be changed.",
		Example:    "moment user approve visitor --data-dir ./moment-data",
		Capability: CapabilityDirectOnly,
		Run:        runAccountStatus(domain.AccountActive),
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "suspend"}, Short: "Suspend an account",
		Long:       "Stop an account from signing in and end every session it currently holds.",
		Inputs:     "One username.",
		Output:     "The updated account.",
		Errors:     "An unknown username returns ACCOUNT_NOT_FOUND; the owner account cannot be changed.",
		Example:    "moment user suspend visitor --data-dir ./moment-data",
		Capability: CapabilityDirectOnly,
		Safety:     "Destructive. Suspending ends every session the account currently holds, so it is signed out everywhere at once.",
		Policy:     SafetyPolicy{Destructive: true, Confirmation: "suspend"},
		Run:        runAccountStatus(domain.AccountSuspended),
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "identities"}, Short: "Show how an account signs in",
		Long:       "List the external identities linked to one account.",
		Inputs:     "One username.",
		Output:     "A JSON list of provider, display_name, and linked/last-login timestamps.",
		Errors:     "An unknown username returns ACCOUNT_NOT_FOUND.",
		Example:    "moment user identities visitor --data-dir ./moment-data --json",
		Capability: CapabilityDirectOnly,
		Run:        runAccountIdentities,
	})
}

// openDirectAccounts binds the account service to a stopped data directory.
//
// These commands are direct-only. Reviewing who may write is exactly the kind
// of decision worth requiring physical access to the data directory for, and
// the API-mode alternative already exists in the browser for an owner who is
// signed in.
func openDirectAccounts(ctx context.Context, invocation Invocation) (*app.AccountService, *sqlitestore.Database, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, nil, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, ownerRuntimeError(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return nil, nil, NewCommandError("DATABASE_NOT_READY", "the current Moment database could not become ready",
			"inspect the selected data directory and migration history, then retry", ExitIntegrity).WithCause(err)
	}
	repository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	service, err := app.NewAccountService(repository)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	return service, database, nil
}

func runAccountList(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	filter, err := input.String("status")
	if err != nil {
		return Result{}, err
	}
	if filter != "" {
		if err := domain.ValidateAccountStatus(domain.AccountStatus(filter)); err != nil {
			return Result{}, accountCLIError(err)
		}
	}
	service, database, err := openDirectAccounts(ctx, invocation)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = database.Close() }()

	users, err := service.List(ctx)
	if err != nil {
		return Result{}, accountCLIError(err)
	}
	items := make([]accountView, 0, len(users))
	lines := make([]string, 0, len(users)+1)
	pending := 0
	for _, user := range users {
		if filter != "" && string(user.Status) != filter {
			continue
		}
		if user.Status == domain.AccountPending {
			pending++
		}
		items = append(items, accountViewOf(user))
		lines = append(lines, fmt.Sprintf("%-12s %-10s %-10s %s", user.Username, user.Role, user.Status, user.DisplayName))
	}
	if pending > 0 {
		lines = append(lines, fmt.Sprintf("\n%d account(s) waiting: moment user approve <username>", pending))
	}
	return Result{
		Data: items,
		Meta: map[string]any{"count": len(items)},
		Text: joinLines(lines),
	}, nil
}

func runAccountStatus(status domain.AccountStatus) func(context.Context, Invocation, CommandInput) (Result, error) {
	return func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
		username, err := singleAccountArgument(input)
		if err != nil {
			return Result{}, err
		}
		service, database, err := openDirectAccounts(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = database.Close() }()

		user, err := service.AccountByUsername(ctx, username)
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		updated, err := service.SetStatus(ctx, user.ID, status)
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		return Result{
			Data: accountViewOf(updated),
			Text: fmt.Sprintf("%s is now %s", updated.Username, updated.Status),
		}, nil
	}
}

func runAccountIdentities(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	username, err := singleAccountArgument(input)
	if err != nil {
		return Result{}, err
	}
	service, database, err := openDirectAccounts(ctx, invocation)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = database.Close() }()

	user, err := service.AccountByUsername(ctx, username)
	if err != nil {
		return Result{}, accountCLIError(err)
	}
	identities, err := service.Identities(ctx, user.ID)
	if err != nil {
		return Result{}, accountCLIError(err)
	}
	type identityView struct {
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name"`
		LinkedAt    string `json:"linked_at"`
		LastLoginAt string `json:"last_login_at"`
	}
	items := make([]identityView, 0, len(identities))
	lines := make([]string, 0, len(identities))
	for _, identity := range identities {
		item := identityView{
			Provider:    identity.Provider,
			DisplayName: identity.DisplayName,
			LinkedAt:    time.UnixMilli(identity.CreatedAtMS).UTC().Format(time.RFC3339),
			LastLoginAt: time.UnixMilli(identity.LastLoginAtMS).UTC().Format(time.RFC3339),
		}
		items = append(items, item)
		lines = append(lines, fmt.Sprintf("%-10s last signed in %s", identity.Provider, item.LastLoginAt))
	}
	if len(items) == 0 {
		lines = append(lines, "no linked sign-in methods")
	}
	return Result{Data: items, Meta: map[string]any{"count": len(items)}, Text: joinLines(lines)}, nil
}

func singleAccountArgument(input CommandInput) (string, error) {
	args := input.Args()
	if len(args) != 1 || args[0] == "" {
		return "", invalidArgument("one username is required", "pass exactly one username")
	}
	return args[0], nil
}

// accountCLIError maps account failures onto the CLI's stable codes.
func accountCLIError(err error) error {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return err
	}
	switch domainErr.Code {
	case domain.CodeAccountNotFound:
		return NewCommandError(string(domainErr.Code), domainErr.Message, "run 'moment user list' to see the accounts on this site", ExitInvalidInput)
	case domain.CodeInvalidArgument, domain.CodeUsernameTaken, domain.CodeUsernameReserved:
		return NewCommandError(string(domainErr.Code), domainErr.Message, "correct the input and retry", ExitInvalidInput)
	default:
		return err
	}
}

func joinLines(lines []string) string {
	out := ""
	for index, line := range lines {
		if index > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
