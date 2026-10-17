package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/accountdto"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerAccountAdministration(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "set-role"}, ArgsUsage: "<username>", Short: "Change an account's administrative role",
		Long:   "Set admin or member without changing content authorship, the site profile, or credential ownership.",
		Inputs: "One username and --role admin|member. Direct mode uses local filesystem authority; API mode requires an active administrator.",
		Output: "The account with its current role and status.", Errors: "LAST_ADMINISTRATOR refuses removal of the last active administrator; invalid roles return INVALID_ARGUMENT.",
		Example: "moment user set-role alice --role admin --data-dir ./moment-data --json", Capability: CapabilityBoth, Args: cobra.ExactArgs(1),
		Flags: func(flags *pflag.FlagSet) { flags.String("role", "", "admin or member") }, Run: runAccountRole,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"site", "set-profile"}, ArgsUsage: "<username>", Short: "Select the site's public display account",
		Long:   "Choose an active account for the public site profile independently of its administrative role.",
		Inputs: "One active username. Direct mode uses local filesystem authority; API mode requires an active administrator.",
		Output: "The current public site settings and selected profile.", Errors: "Missing accounts return ACCOUNT_NOT_FOUND; inactive accounts return INVALID_ARGUMENT.",
		Example: "moment site set-profile alice --data-dir ./moment-data --json", Capability: CapabilityBoth, Args: cobra.ExactArgs(1), Run: runSiteProfile,
	})
}

func runAccountRole(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	username, err := singleAccountArgument(input)
	if err != nil {
		return Result{}, err
	}
	role, err := input.String("role")
	if err != nil {
		return Result{}, err
	}
	if err := domain.ValidateUserRole(domain.UserRole(role)); err != nil {
		return Result{}, accountCLIError(err)
	}
	var updated accountView
	var meta map[string]any
	if invocation.Mode() == ModeDirect {
		accounts, db, err := openDirectAccounts(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = db.Close() }()
		user, err := accounts.AccountByUsername(ctx, username)
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		user, err = accounts.SetRole(ctx, user.ID, domain.UserRole(role))
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		updated = accountViewOf(user)
	} else {
		selected, err := administrativeAccountAPI(ctx, invocation, username)
		if err != nil {
			return Result{}, err
		}
		userID := selected.UserID
		var user accountdto.Private
		meta, err = accountAPIRequest(ctx, invocation, http.MethodPut, "/api/v1/accounts/"+userID+"/role", map[string]string{"role": role}, http.StatusOK, &user)
		if err != nil {
			return Result{}, err
		}
		if user.UserID != userID || user.Role != role {
			return Result{}, invalidAPIResponse()
		}
		updated = accountView{UserID: user.UserID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, Status: user.Status, ProfileURL: user.ProfileURL}
	}
	return Result{Data: updated, Meta: meta, Text: fmt.Sprintf("%s now has role %s", updated.Username, updated.Role)}, nil
}

func runSiteProfile(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	username, err := singleAccountArgument(input)
	if err != nil {
		return Result{}, err
	}
	var settings app.SiteSettings
	var meta map[string]any
	if invocation.Mode() == ModeDirect {
		accounts, db, err := openDirectAccounts(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		user, err := accounts.AccountByUsername(ctx, username)
		if err == nil {
			_, err = accounts.SelectSiteProfile(ctx, user.ID)
		}
		closeErr := db.Close()
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		if closeErr != nil {
			return Result{}, closeErr
		}
		settings, err = directSiteGet(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
	} else {
		user, err := administrativeAccountAPI(ctx, invocation, username)
		if err != nil {
			return Result{}, err
		}
		meta, err = siteAPIRequest(ctx, invocation, http.MethodPut, "/api/v1/site/profile", http.StatusOK, map[string]string{"user_id": user.UserID}, true, &settings)
		if err != nil {
			return Result{}, err
		}
		if settings.ProfileUserID != user.UserID {
			return Result{}, invalidAPIResponse()
		}
	}
	result := siteResult("Site display account selected", settings)
	result.Meta = meta
	return result, nil
}

func administrativeAccountAPI(ctx context.Context, invocation Invocation, username string) (accountdto.Private, error) {
	var roster []accountdto.Private
	if _, err := accountAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/accounts", nil, http.StatusOK, &roster); err != nil {
		return accountdto.Private{}, err
	}
	for _, user := range roster {
		if user.Username == username {
			if domain.ValidateUserID(user.UserID) != nil {
				return accountdto.Private{}, invalidAPIResponse()
			}
			return user, nil
		}
	}
	return accountdto.Private{}, canonicalPublicError(string(domain.CodeAccountNotFound), "no such account", nil, http.StatusNotFound)
}
