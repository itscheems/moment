package cli

import (
	"context"
	"net/http"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/accountdto"
	"github.com/spf13/pflag"
)

func registerPreferences(application *Application) {
	for _, update := range []bool{false, true} {
		verb, short := "get-preferences", "Read your private account preferences"
		if update {
			verb, short = "update-preferences", "Save your private account preferences"
		}
		spec := LeafSpec{
			Path: []string{"user", verb}, Short: short,
			Long:       "Read or save the account's interface language independently of its public profile.",
			Inputs:     "Direct mode requires --as <username>. API mode uses the authenticated account. Updates require --ui-locale zh-CN|en|auto; auto clears the explicit choice.",
			Output:     "Private preferences containing ui_locale (zh-CN, en, or null).",
			Errors:     "Unsupported languages return INVALID_ARGUMENT; a missing account identity returns UNAUTHORIZED.",
			Example:    "moment --as writer user " + verb + " --data-dir ./moment-data --json",
			Capability: CapabilityBoth,
			Run:        runPreferences(update),
		}
		if update {
			spec.Example = "moment --as writer user update-preferences --ui-locale en --data-dir ./moment-data --json"
			spec.Flags = func(flags *pflag.FlagSet) { flags.String("ui-locale", "", "interface language: zh-CN, en, or auto") }
		}
		application.MustRegisterLeaf(spec)
	}
}

func runPreferences(update bool) func(context.Context, Invocation, CommandInput) (Result, error) {
	return func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
		var locale *string
		if update {
			if !input.Changed("ui-locale") {
				return Result{}, invalidArgument("ui-locale is required", "set --ui-locale zh-CN, en, or auto")
			}
			value, err := input.String("ui-locale")
			if err != nil {
				return Result{}, err
			}
			if value != "auto" {
				locale = &value
			}
			if err := domain.ValidateUILocale(locale); err != nil {
				return Result{}, accountCLIError(err)
			}
		}
		var preferences accountdto.Preferences
		var meta map[string]any
		if invocation.Mode() == ModeDirect {
			actor, ok := app.ContentActor(ctx)
			if !ok {
				return Result{}, canonicalPublicError(string(domain.CodeUnauthorized), "an account identity is required", nil, 0)
			}
			accounts, db, err := openDirectAccounts(ctx, invocation)
			if err != nil {
				return Result{}, err
			}
			defer func() { _ = db.Close() }()
			var user domain.User
			if update {
				user, err = accounts.UpdatePreferences(ctx, actor.ID, locale)
			} else {
				user, err = accounts.AccountByUsername(ctx, actor.Username)
			}
			if err != nil {
				return Result{}, accountCLIError(err)
			}
			preferences = accountdto.PreferencesFromUser(user)
		} else {
			method := http.MethodGet
			var payload any
			if update {
				method, payload = http.MethodPatch, accountdto.Preferences{UILocale: locale}
			}
			var err error
			meta, err = accountAPIRequest(ctx, invocation, method, "/api/v1/account/preferences", payload, http.StatusOK, &preferences)
			if err != nil {
				return Result{}, err
			}
			if domain.ValidateUILocale(preferences.UILocale) != nil {
				return Result{}, invalidAPIResponse()
			}
		}
		label := "auto"
		if preferences.UILocale != nil {
			label = *preferences.UILocale
		}
		return Result{Data: preferences, Meta: meta, Text: "Interface language: " + label}, nil
	}
}
