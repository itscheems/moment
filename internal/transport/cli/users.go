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

	"github.com/moment/moment/internal/app"
	"github.com/spf13/pflag"
)

func registerUsers(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "suggest"}, Short: "Search users for an @mention",
		Long:       "Return bounded public identities ranked by the same lightweight service used by the browser composer.",
		Inputs:     "An optional --query username or display-name fragment and --limit from 1 to 20. Public API mode requires no owner token.",
		Output:     "A stable JSON list containing immutable owner_id, username, display_name, avatar_media_id, and profile_url.",
		Errors:     "Invalid limits return INVALID_ARGUMENT; unavailable APIs use dependency exit codes.",
		Example:    "moment user suggest --data-dir ./moment-data --query che --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("query", "", "username or display-name fragment")
			flags.Int("limit", 8, "maximum suggestions from 1 to 20")
		},
		Run: runUserSuggest,
	})
}

func runUserSuggest(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	query, err := input.String("query")
	if err != nil {
		return Result{}, err
	}
	limit, err := input.Int("limit")
	if err != nil {
		return Result{}, err
	}
	var items []app.MentionSuggestion
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		service, database, openErr := openDirectSite(ctx, invocation)
		if openErr != nil {
			return Result{}, openErr
		}
		defer func() { _ = database.Close() }()
		items, err = service.SuggestMentions(ctx, query, limit)
	case ModeAPI:
		params := url.Values{"q": []string{query}, "limit": []string{fmt.Sprint(limit)}}
		meta, err = publicListAPIRequest(ctx, invocation, "/api/v1/users/mention-suggestions", params, &items)
	default:
		err = errors.New("unsupported user suggestion transport")
	}
	if err != nil {
		return Result{}, siteCLIError(err)
	}
	return Result{Data: items, Meta: meta, Text: fmt.Sprintf("%d mention suggestion(s)", len(items))}, nil
}

func publicListAPIRequest(ctx context.Context, invocation Invocation, endpoint string, query url.Values, destination any) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	base, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
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
	data, meta, err := decodeStrictAPIEnvelope(body)
	if err != nil {
		return nil, invalidAPIResponse()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}
