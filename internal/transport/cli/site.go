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
	"github.com/moment/moment/internal/capabilities"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/spf13/pflag"
)

func registerSite(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"site", "capabilities"}, Short: "Read effective content and media limits",
		Long:       "Read the same authoritative limits used by the server; text lengths are Unicode code points.",
		Inputs:     "Direct mode selects --data-dir. API mode uses the public capabilities endpoint.",
		Output:     "Version, text_length_unit, and limits for content, media, and pagination.",
		Errors:     "Unavailable instances or APIs return a stable dependency or integrity error.",
		Example:    "moment site capabilities --data-dir ./moment-data --json",
		Capability: CapabilityBoth, Run: runCapabilities,
	})
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
		Inputs:     "Set --title, --timezone, --public-base-url, --comments-enabled or --registration-mode. Use --clear-public-base-url to clear the canonical URL. Personal fields belong to user update-profile.",
		Output:     "The updated public site settings.",
		Errors:     "Invalid site configuration returns INVALID_ARGUMENT; API mode requires an administrator.",
		Example:    "moment site update --data-dir ./moment-data --title 'My Moment' --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("title", "", "set the site title")
			flags.String("timezone", "", "set an IANA timezone")
			flags.String("public-base-url", "", "set the canonical public URL")
			flags.Bool("clear-public-base-url", false, "clear the canonical public URL")
			flags.Bool("comments-enabled", true, "enable or disable new comments")
			flags.String("registration-mode", "", "open, approval or closed")
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
		meta, err = siteAPIRequest(ctx, invocation, http.MethodPatch, "/api/v1/site", http.StatusOK, payload, true, &settings)
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
	payload := map[string]any{}
	for _, field := range []struct {
		flag, wire string
		set        func(*string)
	}{
		{"title", "title", func(v *string) { update.Title = v }},
		{"timezone", "timezone", func(v *string) { update.Timezone = v }},
		{"registration-mode", "registration_mode", func(v *string) { update.RegistrationMode = v }},
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
	clear, err := input.Bool("clear-public-base-url")
	if err != nil {
		return update, nil, err
	}
	if clear && input.Changed("public-base-url") {
		return update, nil, invalidArgument("URL selection and clearing are mutually exclusive", "choose one URL operation")
	}
	if clear {
		update.PublicBaseURL = &app.NullableString{}
		payload["public_base_url"] = nil
	} else if input.Changed("public-base-url") {
		value, err := input.String("public-base-url")
		if err != nil {
			return update, nil, err
		}
		update.PublicBaseURL = &app.NullableString{Value: &value}
		payload["public_base_url"] = value
	}
	if input.Changed("comments-enabled") {
		value, err := input.Bool("comments-enabled")
		if err != nil {
			return update, nil, err
		}
		update.CommentsEnabled = &value
		payload["comments_enabled"] = value
	}
	if update.Empty() {
		return update, nil, invalidArgument("site update requires at least one setting", "provide a site configuration field")
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
	return directSiteWrite(ctx, invocation, func(service *app.SiteService) (app.SiteSettings, error) {
		return service.Update(ctx, update)
	})
}

func directSitePinChange(ctx context.Context, invocation Invocation, id domain.ID, pin bool) (app.SiteSettings, error) {
	return directSiteWrite(ctx, invocation, func(service *app.SiteService) (app.SiteSettings, error) {
		if pin {
			return service.Pin(ctx, id)
		}
		return service.Unpin(ctx, id)
	})
}

func directSiteWrite(ctx context.Context, invocation Invocation, mutate func(*app.SiteService) (app.SiteSettings, error)) (app.SiteSettings, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return app.SiteSettings{}, err
	}
	runtimeState, lease, err := runtimefs.OpenForWriteContext(ctx, target.DataDirectory())
	if err != nil {
		return app.SiteSettings{}, runtimeError(err)
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
	return mutate(service)
}

func openDirectSite(ctx context.Context, invocation Invocation) (*app.SiteService, *sqlitestore.Database, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return nil, nil, err
	}
	runtimeState, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		return nil, nil, runtimeError(err)
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

func requestAPIJSON(
	ctx context.Context,
	invocation Invocation,
	method string,
	endpoint string,
	expectedStatus int,
	payload any,
	authenticate bool,
	destination any,
) (json.RawMessage, map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, nil, err
	}
	route, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), route.Path)
	if err != nil {
		return nil, nil, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	if route.RawQuery != "" {
		endpointURL += "?" + route.RawQuery
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpointURL, body)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticate {
		token, err := readAPIToken(target)
		if err != nil {
			return nil, nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, nil, err
	}
	if len(responseBody) > maxAPIResponse {
		return nil, nil, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the Moment API response exceeded the allowed size", "check the server and reverse proxy configuration", ExitUnavailable)
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return nil, nil, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, decodeAPIFailure(response.StatusCode, responseBody)
	}
	if response.StatusCode != expectedStatus {
		return nil, nil, invalidAPIResponse()
	}
	data, meta, err := decodeStrictAPISuccess(responseBody, destination)
	if err != nil {
		return nil, nil, err
	}
	return data, meta, nil
}

func validSiteAPIData(data json.RawMessage, settings app.SiteSettings) bool {
	if !hasRequiredAPIDataFields(data, "profile_user_id", "username", "display_name", "title", "bio", "timezone", "public_base_url", "avatar_media_id", "cover_media_id", "pinned_post_id", "comments_enabled") ||
		!hasRequiredNonNullAPIDataFields(data, "profile_user_id", "username", "display_name", "title", "bio", "timezone", "comments_enabled") ||
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
	if settings.PinnedPostID != nil {
		id, err := domain.ParseID(*settings.PinnedPostID)
		if err != nil || id.String() != *settings.PinnedPostID {
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
	pinned := "not configured"
	if settings.PinnedPostID != nil {
		pinned = *settings.PinnedPostID
	}
	comments := "disabled"
	if settings.CommentsEnabled {
		comments = "enabled"
	}
	return Result{
		Data: settings,
		Text: fmt.Sprintf("%s.\nTitle: %s\nTimezone: %s\nPublic URL: %s\nAvatar: %s\nCover: %s\nPinned post: %s\nComments: %s", action, settings.Title, settings.Timezone, publicURL, avatar, cover, pinned, comments),
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

func runCapabilities(ctx context.Context, invocation Invocation, _ CommandInput) (Result, error) {
	var value capabilities.Description
	var meta map[string]any
	if invocation.Mode() == ModeDirect {
		direct, err := openDirectMedia(ctx, invocation)
		if err != nil {
			return Result{}, mediaCLIError(err)
		}
		defer func() { _ = direct.Close() }()
		value = capabilities.Describe(direct.store)
	} else {
		var err error
		_, meta, err = requestAPIJSON(ctx, invocation, http.MethodGet, "/api/v1/capabilities", http.StatusOK, nil, false, &value)
		if err != nil {
			return Result{}, siteCLIError(err)
		}
		if !value.Valid() {
			return Result{}, invalidAPIResponse()
		}
	}
	return Result{Data: value, Meta: meta, Text: fmt.Sprintf("Content limits: %d code points per post, %d per comment", value.Limits.MaxPostCodePoints, value.Limits.MaxCommentCodePoints)}, nil
}

func siteAPIRequest(ctx context.Context, invocation Invocation, method, endpoint string, expectedStatus int, payload any, authenticate bool, destination any) (map[string]any, error) {
	data, meta, err := requestAPIJSON(ctx, invocation, method, endpoint, expectedStatus, payload, authenticate, destination)
	if err != nil {
		return nil, err
	}
	settings, ok := destination.(*app.SiteSettings)
	if !ok || !validSiteAPIData(data, *settings) {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}
