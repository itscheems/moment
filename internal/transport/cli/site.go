package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/spf13/pflag"
)

func registerSite(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"site", "get"},
		Short:      "Read portable public site settings",
		Long:       "Read the public site identity from the selected local instance or HTTP API without deriving values from request headers.",
		Inputs:     "Direct mode reads --data-dir. API mode reads the public endpoint and does not require an owner token.",
		Output:     "Title, bio, IANA timezone, normalized public base URL, nullable managed avatar and cover IDs, and comments state.",
		Errors:     "Missing or invalid authority data returns DATA_INTEGRITY_FAILED; unavailable APIs use dependency exit codes.",
		Example:    "moment site get --data-dir ./moment-data --json",
		Capability: CapabilityBoth,
		Run:        runSiteGet,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"site", "update"},
		Short:      "Partially update portable site settings",
		Long:       "Atomically validate and update only the explicitly supplied site identity fields through the selected transport.",
		Inputs:     "Set fields with --title, --bio, --timezone, --public-base-url, --avatar-media-id, --cover-media-id, and --comments-enabled. Clear nullable fields with --clear-public-base-url, --clear-avatar, or --clear-cover. API mode authenticates through MOMENT_TOKEN or --token-file.",
		Output:     "The complete updated public settings DTO, equivalent in direct and API modes.",
		Errors:     "Invalid IANA zones or URLs return INVALID_ARGUMENT; missing managed media returns MEDIA_NOT_FOUND; API authentication failures return UNAUTHORIZED.",
		Example:    "moment site update --data-dir ./moment-data --title 'Jojo moments' --timezone Asia/Shanghai --comments-enabled=false --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("title", "", "set the public site title")
			flags.String("bio", "", "set the public site biography")
			flags.String("timezone", "", "set an IANA timezone such as Asia/Shanghai")
			flags.String("public-base-url", "", "set the canonical absolute public HTTP(S) base URL")
			flags.Bool("clear-public-base-url", false, "clear the configured public base URL")
			flags.String("avatar-media-id", "", "set the managed media ID used as the site avatar")
			flags.Bool("clear-avatar", false, "clear the site avatar")
			flags.String("cover-media-id", "", "set the managed media ID used as the profile cover")
			flags.Bool("clear-cover", false, "clear the profile cover")
			flags.Bool("comments-enabled", true, "enable or disable new public comments")
		},
		Run: runSiteUpdate,
	})
}

func runSiteGet(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	var (
		settings app.SiteSettings
		meta     map[string]any
		err      error
	)
	switch invocation.Mode() {
	case ModeDirect:
		settings, err = directSiteGet(ctx, invocation)
	case ModeAPI:
		meta, err = siteAPIRequest(ctx, invocation, http.MethodGet, "/api/v1/site", http.StatusOK, nil, false, &settings)
	default:
		err = errors.New("unsupported site get transport")
	}
	if err != nil {
		return Result{}, siteCLIError(err)
	}
	result := siteResult("Site settings", settings)
	result.Meta = meta
	return result, nil
}

func runSiteUpdate(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	update, payload, err := siteUpdateInput(input)
	if err != nil {
		return Result{}, err
	}
	var (
		settings app.SiteSettings
		meta     map[string]any
	)
	switch invocation.Mode() {
	case ModeDirect:
		settings, err = directSiteUpdate(ctx, invocation, update)
	case ModeAPI:
		meta, err = siteAPIRequest(ctx, invocation, http.MethodPatch, "/api/v1/owner/site", http.StatusOK, payload, true, &settings)
	default:
		err = errors.New("unsupported site update transport")
	}
	if err != nil {
		return Result{}, siteCLIError(err)
	}
	result := siteResult("Site settings updated", settings)
	result.Meta = meta
	return result, nil
}

func siteUpdateInput(input CommandInput) (app.SiteUpdate, map[string]any, error) {
	update := app.SiteUpdate{}
	payload := make(map[string]any)
	for _, field := range []struct {
		flag string
		set  func(string)
	}{
		{flag: "title", set: func(value string) { update.Title = &value; payload["title"] = value }},
		{flag: "bio", set: func(value string) { update.Bio = &value; payload["bio"] = value }},
		{flag: "timezone", set: func(value string) { update.Timezone = &value; payload["timezone"] = value }},
	} {
		if input.Changed(field.flag) {
			value, err := input.String(field.flag)
			if err != nil {
				return app.SiteUpdate{}, nil, err
			}
			field.set(value)
		}
	}
	publicChanged := input.Changed("public-base-url")
	clearPublic, err := input.Bool("clear-public-base-url")
	if err != nil {
		return app.SiteUpdate{}, nil, err
	}
	if publicChanged && clearPublic {
		return app.SiteUpdate{}, nil, invalidArgument("--public-base-url and --clear-public-base-url are mutually exclusive", "choose either a canonical URL or an explicit clear")
	}
	if publicChanged {
		value, err := input.String("public-base-url")
		if err != nil {
			return app.SiteUpdate{}, nil, err
		}
		update.PublicBaseURL = &app.NullableString{Value: &value}
		payload["public_base_url"] = value
	} else if clearPublic {
		update.PublicBaseURL = &app.NullableString{}
		payload["public_base_url"] = nil
	}
	avatarChanged := input.Changed("avatar-media-id")
	clearAvatar, err := input.Bool("clear-avatar")
	if err != nil {
		return app.SiteUpdate{}, nil, err
	}
	if avatarChanged && clearAvatar {
		return app.SiteUpdate{}, nil, invalidArgument("--avatar-media-id and --clear-avatar are mutually exclusive", "choose either a managed media ID or an explicit clear")
	}
	if avatarChanged {
		value, err := input.String("avatar-media-id")
		if err != nil {
			return app.SiteUpdate{}, nil, err
		}
		update.AvatarMediaID = &app.NullableString{Value: &value}
		payload["avatar_media_id"] = value
	} else if clearAvatar {
		update.AvatarMediaID = &app.NullableString{}
		payload["avatar_media_id"] = nil
	}
	coverChanged := input.Changed("cover-media-id")
	clearCover, err := input.Bool("clear-cover")
	if err != nil {
		return app.SiteUpdate{}, nil, err
	}
	if coverChanged && clearCover {
		return app.SiteUpdate{}, nil, invalidArgument("--cover-media-id and --clear-cover are mutually exclusive", "choose either a managed media ID or an explicit clear")
	}
	if coverChanged {
		value, err := input.String("cover-media-id")
		if err != nil {
			return app.SiteUpdate{}, nil, err
		}
		update.CoverMediaID = &app.NullableString{Value: &value}
		payload["cover_media_id"] = value
	} else if clearCover {
		update.CoverMediaID = &app.NullableString{}
		payload["cover_media_id"] = nil
	}
	if input.Changed("comments-enabled") {
		value, err := input.Bool("comments-enabled")
		if err != nil {
			return app.SiteUpdate{}, nil, err
		}
		update.CommentsEnabled = &value
		payload["comments_enabled"] = value
	}
	if update.Empty() {
		return app.SiteUpdate{}, nil, invalidArgument("site update requires at least one setting flag", "provide a field to set or an explicit clear flag")
	}
	return update, payload, nil
}

func directSiteGet(ctx context.Context, invocation Invocation) (app.SiteSettings, error) {
	service, database, err := openDirectSite(ctx, invocation)
	if err != nil {
		return app.SiteSettings{}, err
	}
	defer func() { _ = database.Close() }()
	return service.Get(ctx)
}

func directSiteUpdate(ctx context.Context, invocation Invocation, update app.SiteUpdate) (app.SiteSettings, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return app.SiteSettings{}, err
	}
	runtimeState, lease, err := runtimefs.OpenForWriteContext(ctx, target.DataDirectory())
	if err != nil {
		return app.SiteSettings{}, ownerRuntimeError(err)
	}
	defer func() { _ = lease.Release() }()
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return app.SiteSettings{}, NewCommandError("DATABASE_NOT_READY", "the current Moment database could not become ready", "inspect the selected data directory and migration history, then retry", ExitIntegrity).WithCause(err)
	}
	defer func() { _ = database.Close() }()
	repository, err := sqlitestore.NewSiteRepositoryWithinMaintenance(database, lease, runtimeState.Locks)
	if err != nil {
		return app.SiteSettings{}, err
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: runtimeState.IsDevelopment()})
	if err != nil {
		return app.SiteSettings{}, err
	}
	return service.Update(ctx, update)
}

func openDirectSite(ctx context.Context, invocation Invocation) (*app.SiteService, *sqlitestore.Database, error) {
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
		return nil, nil, NewCommandError("DATABASE_NOT_READY", "the current Moment database could not become ready", "inspect the selected data directory and migration history, then retry", ExitIntegrity).WithCause(err)
	}
	repository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: runtimeState.IsDevelopment()})
	if err != nil {
		_ = database.Close()
		return nil, nil, err
	}
	return service, database, nil
}

func siteAPIRequest(
	ctx context.Context,
	invocation Invocation,
	method string,
	endpoint string,
	expectedStatus int,
	payload any,
	authenticate bool,
	destination any,
) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpointURL, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticate {
		token, err := readAPIToken(target)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > maxAPIResponse {
		return nil, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the Moment API response exceeded the allowed size", "check the server and reverse proxy configuration", ExitUnavailable)
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	if response.StatusCode != expectedStatus {
		return nil, invalidAPIResponse()
	}
	data, meta, err := decodeStrictAPISuccess(responseBody, destination)
	if err != nil {
		return nil, err
	}
	settings, ok := destination.(*app.SiteSettings)
	if !ok || !validSiteAPIData(data, *settings) {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func validSiteAPIData(data json.RawMessage, settings app.SiteSettings) bool {
	if !hasRequiredAPIDataFields(data, "title", "bio", "timezone", "public_base_url", "avatar_media_id", "cover_media_id", "comments_enabled") ||
		!hasRequiredNonNullAPIDataFields(data, "title", "bio", "timezone", "comments_enabled") ||
		settings.Timezone == "" || settings.Timezone == "Local" || strings.HasPrefix(settings.Timezone, "/") || strings.Contains(settings.Timezone, "..") {
		return false
	}
	if _, err := time.LoadLocation(settings.Timezone); err != nil {
		return false
	}
	if settings.PublicBaseURL != nil {
		normalized, err := app.NormalizePublicBaseURL(*settings.PublicBaseURL, true)
		if err != nil || normalized != *settings.PublicBaseURL {
			return false
		}
	}
	if settings.AvatarMediaID != nil {
		id, err := domain.ParseID(*settings.AvatarMediaID)
		if err != nil || id.String() != *settings.AvatarMediaID {
			return false
		}
	}
	if settings.CoverMediaID != nil {
		id, err := domain.ParseID(*settings.CoverMediaID)
		if err != nil || id.String() != *settings.CoverMediaID {
			return false
		}
	}
	return true
}

func siteResult(action string, settings app.SiteSettings) Result {
	publicURL := "not configured"
	if settings.PublicBaseURL != nil {
		publicURL = *settings.PublicBaseURL
	}
	avatar := "not configured"
	if settings.AvatarMediaID != nil {
		avatar = *settings.AvatarMediaID
	}
	cover := "not configured"
	if settings.CoverMediaID != nil {
		cover = *settings.CoverMediaID
	}
	comments := "disabled"
	if settings.CommentsEnabled {
		comments = "enabled"
	}
	return Result{
		Data: settings,
		Text: fmt.Sprintf("%s.\nTitle: %s\nTimezone: %s\nPublic URL: %s\nAvatar: %s\nCover: %s\nComments: %s", action, settings.Title, settings.Timezone, publicURL, avatar, cover, comments),
	}
}

func siteCLIError(err error) error {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return err
	}
	message := strings.TrimSpace(domainErr.Message)
	if message == "" {
		message = "the site settings operation failed"
	}
	return canonicalPublicError(string(domainErr.Code), message, domainErr.Details, 0).WithCause(err)
}
