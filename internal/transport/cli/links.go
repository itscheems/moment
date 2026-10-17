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

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/transport/linkdto"
	"github.com/spf13/cobra"
)

func registerLinks(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"link", "unfurl"}, Short: "Capture a link preview card",
		Long:       "Fetch one URL through Moment's guarded outbound fetcher and return the canonical link card snapshot.",
		Inputs:     "Provide one absolute http or https URL as the only argument. Direct mode captures the thumbnail into managed media; the fetcher refuses non-public destinations and revalidates every redirect hop.",
		Output:     "One canonical link card object suitable for --link-json.",
		Errors:     "Unreachable or unusable URLs return LINK_PREVIEW_UNAVAILABLE; malformed URLs return INVALID_ARGUMENT.",
		Example:    "moment link unfurl 'https://www.youtube.com/watch?v=dQw4w9WgXcQ' --data-dir ./moment-data --json",
		Capability: CapabilityBoth,
		Args:       cobra.ExactArgs(1),
		Run:        runLinkUnfurl,
	})
}

func runLinkUnfurl(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	arguments := input.Args()
	if len(arguments) != 1 || strings.TrimSpace(arguments[0]) == "" {
		return Result{}, invalidArgument("one URL argument is required", "provide one absolute http or https URL")
	}
	raw := arguments[0]
	card, meta, err := unfurlLinkURL(ctx, invocation, raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: card, Text: linkCardText(card), Meta: meta}, nil
}

// unfurlLinkURL is shared by `link unfurl` and the `post create --link`
// convenience form so both transports produce byte-identical snapshots.
func unfurlLinkURL(ctx context.Context, invocation Invocation, raw string) (linkdto.Card, map[string]any, error) {
	var card linkdto.Card
	var meta map[string]any
	var err error
	switch invocation.Mode() {
	case ModeDirect:
		direct, openErr := openDirectLinkContext(ctx, invocation)
		if openErr != nil {
			return linkdto.Card{}, nil, openErr
		}
		defer direct.Close()
		preview, resolveErr := direct.service.Resolve(ctx, raw)
		if resolveErr != nil {
			return linkdto.Card{}, nil, linkCLIError(resolveErr)
		}
		card = linkdto.FromPreview(preview)
	case ModeAPI:
		values := url.Values{}
		values.Set("url", raw)
		meta, err = linkAPIRequest(ctx, invocation, "/api/v1/links/unfurl", values, &card)
		if err != nil {
			return linkdto.Card{}, nil, linkCLIError(err)
		}
	default:
		return linkdto.Card{}, nil, errors.New("unsupported link transport")
	}
	if _, err := card.Domain(0); err != nil {
		return linkdto.Card{}, nil, invalidAPIResponse()
	}
	return card, meta, nil
}

type directLinkContext struct {
	service *app.LinkPreviewService
	runtime *directMediaRuntime
}

func (context directLinkContext) Close() {
	if context.runtime != nil {
		_ = context.runtime.Close()
	}
}

// openDirectLinkContext assembles the same guarded resolver and managed media
// store the server uses, so a direct unfurl captures its thumbnail exactly the
// way the HTTP path does.
func openDirectLinkContext(ctx context.Context, invocation Invocation) (directLinkContext, error) {
	runtime, err := openDirectMedia(ctx, invocation)
	if err != nil {
		return directLinkContext{}, err
	}
	service, err := app.NewLinkPreviewService(
		linkfetch.NewResolver(linkfetch.ResolverOptions{}),
		media.NewLinkThumbnailAdapter(runtime.store),
		app.LinkPreviewOptions{},
	)
	if err != nil {
		_ = runtime.Close()
		return directLinkContext{}, err
	}
	return directLinkContext{service: service, runtime: runtime}, nil
}

func linkAPIRequest(ctx context.Context, invocation Invocation, endpoint string, query url.Values, destination *linkdto.Card) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	endpointURL, err := url.JoinPath(target.BaseURL().String(), endpoint)
	if err != nil {
		return nil, invalidArgument("the API endpoint URL is invalid", "check --url and retry")
	}
	parsed, _ := url.Parse(endpointURL)
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil || len(body) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(body) != nil {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeAPIFailure(response.StatusCode, body)
	}
	data, meta, err := decodeStrictAPIEnvelope(body)
	if err != nil {
		return nil, invalidAPIResponse()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(destination) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func linkCardText(card linkdto.Card) string {
	lines := []string{fmt.Sprintf("%s — %s", card.Title, card.SiteName)}
	lines = append(lines, card.CanonicalURL)
	if card.EmbedURL != nil {
		lines = append(lines, "embeddable")
	}
	if card.ThumbnailMediaID != nil {
		lines = append(lines, "thumbnail "+*card.ThumbnailMediaID)
	}
	return strings.Join(lines, "\n")
}

func linkCLIError(err error) error {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		exit := ExitInvalidInput
		if domainErr.Code == domain.CodeLinkPreviewUnavailable {
			exit = ExitUnavailable
		}
		return NewCommandError(string(domainErr.Code), domainErr.Message, "check the URL and network reachability, then retry", exit).WithCause(err)
	}
	return err
}
