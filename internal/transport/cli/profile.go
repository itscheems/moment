package cli

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/accountdto"
	"github.com/spf13/pflag"
	"net/http"
)

func registerProfile(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "update-profile"}, Short: "Update your account profile",
		Long:   "Change the current account's public identity independently of site settings and administrative role.",
		Inputs: "Direct mode requires --as <username>. API mode uses the authenticated account. Set --username, --display-name, --bio, --avatar-media-id or --cover-media-id; clear pictures with --clear-avatar or --clear-cover.",
		Output: "The current account's profile, role and status.", Errors: "Invalid names or media return stable validation errors; missing direct identity returns UNAUTHORIZED.",
		Example: "moment --as alice user update-profile --display-name Alice --data-dir ./moment-data --json", Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("username", "", "set your public username")
			flags.String("display-name", "", "set your display name")
			flags.String("bio", "", "set your biography")
			flags.String("avatar-media-id", "", "set your managed avatar")
			flags.String("cover-media-id", "", "set your managed cover")
			flags.Bool("clear-avatar", false, "clear your avatar")
			flags.Bool("clear-cover", false, "clear your cover")
		}, Run: runProfileUpdate,
	})
}

func profileUpdateInput(input CommandInput) (app.ProfileUpdate, map[string]any, error) {
	update := app.ProfileUpdate{}
	payload := map[string]any{}
	for _, field := range []struct {
		flag, wire string
		set        func(*string)
	}{
		{"username", "username", func(v *string) { update.Username = v }},
		{"display-name", "display_name", func(v *string) { update.DisplayName = v }},
		{"bio", "bio", func(v *string) { update.Bio = v }},
	} {
		if input.Changed(field.flag) {
			value, err := input.String(field.flag)
			if err != nil {
				return update, nil, err
			}
			field.set(&value)
			payload[field.wire] = value
		}
	}
	for _, field := range []struct {
		flag, clear, wire string
		set               func(*app.NullableString)
	}{
		{"avatar-media-id", "clear-avatar", "avatar_media_id", func(v *app.NullableString) { update.AvatarMediaID = v }},
		{"cover-media-id", "clear-cover", "cover_media_id", func(v *app.NullableString) { update.CoverMediaID = v }},
	} {
		clear, err := input.Bool(field.clear)
		if err != nil {
			return update, nil, err
		}
		if clear && input.Changed(field.flag) {
			return update, nil, invalidArgument("picture selection and clearing are mutually exclusive", "choose one picture operation")
		}
		if clear {
			field.set(&app.NullableString{})
			payload[field.wire] = nil
		} else if input.Changed(field.flag) {
			value, err := input.String(field.flag)
			if err != nil {
				return update, nil, err
			}
			field.set(&app.NullableString{Value: &value})
			payload[field.wire] = value
		}
	}
	if len(payload) == 0 {
		return update, nil, invalidArgument("at least one profile field is required", "provide a profile field or an explicit picture clear")
	}
	return update, payload, nil
}

func runProfileUpdate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	update, payload, err := profileUpdateInput(input)
	if err != nil {
		return Result{}, err
	}
	var profile accountdto.Private
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
		user, err := accounts.UpdateProfile(ctx, actor.ID, update)
		if err != nil {
			return Result{}, accountCLIError(err)
		}
		profile = accountdto.PrivateFromUser(user)
	} else {
		meta, err = accountAPIRequest(ctx, invocation, http.MethodPatch, "/api/v1/account/profile", payload, http.StatusOK, &profile)
		if err != nil {
			return Result{}, err
		}
		if domain.ValidateUserID(profile.UserID) != nil {
			return Result{}, invalidAPIResponse()
		}
	}
	return Result{Data: profile, Meta: meta, Text: "Account profile updated"}, nil
}
