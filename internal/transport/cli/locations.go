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
	"strconv"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	locationprovider "github.com/moment/moment/internal/location"
	"github.com/moment/moment/internal/transport/locationdto"
	"github.com/spf13/pflag"
)

func registerLocations(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"location", "search"}, Short: "Search selectable post locations",
		Long:   "Search normalized location candidates through configured providers in direct mode or the account HTTP API.",
		Inputs: "Provide --query and optionally --scope auto|global. Global scope queries all configured providers without device bias; auto may use --latitude and --longitude. Provider credentials come from AMAP_API_KEY and GEOAPIFY_API_KEY in direct mode.",
		Output: "A canonical location result array suitable for --location-json.",
		Errors: "Invalid coordinates and unavailable providers use stable codes.",
		Example: "moment location search --query '静安寺' --latitude 31.223 --longitude 121.445 --json\n" +
			"moment location search --query Tokyo --scope global --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("query", "", "location or place query")
			flags.String("scope", "auto", "auto regional search or global search without device bias")
			flags.Float64("latitude", 0, "optional WGS84 latitude bias")
			flags.Float64("longitude", 0, "optional WGS84 longitude bias")
			flags.Int("limit", 0, "result count from 1 to 20 (default 8)")
		}, Run: runLocationSearch,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"location", "nearby"}, Short: "Find places near WGS84 coordinates",
		Long:       "Resolve browser or device coordinates into normalized nearby location candidates.",
		Inputs:     "Provide both --latitude and --longitude in WGS84 decimal degrees.",
		Output:     "A canonical location result array suitable for --location-json.",
		Errors:     "Invalid coordinates and unavailable providers use stable codes.",
		Example:    "moment location nearby --latitude 31.223 --longitude 121.445 --json",
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.Float64("latitude", 0, "required WGS84 latitude")
			flags.Float64("longitude", 0, "required WGS84 longitude")
			flags.Int("limit", 0, "result count from 1 to 20 (default 8)")
		}, Run: runLocationNearby,
	})
}

func runLocationSearch(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	query, err := input.String("query")
	if err != nil || strings.TrimSpace(query) == "" {
		return Result{}, invalidArgument("--query is required", "provide a location or place name")
	}
	point, err := optionalCLILocationPoint(input)
	if err != nil {
		return Result{}, err
	}
	limit, err := input.Int("limit")
	if err != nil {
		return Result{}, err
	}
	return resolveLocationCommand(ctx, invocation, "/api/v1/locations/search", query, point, limit, false, input)
}

func runLocationNearby(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	point, err := requiredCLILocationPoint(input)
	if err != nil {
		return Result{}, err
	}
	limit, err := input.Int("limit")
	if err != nil {
		return Result{}, err
	}
	return resolveLocationCommand(ctx, invocation, "/api/v1/locations/nearby", "", point, limit, true, input)
}

func resolveLocationCommand(ctx context.Context, invocation Invocation, endpoint, query string, point *domain.GeoPoint, limit int, nearby bool, input CommandInput) (Result, error) {
	scope := "auto"
	if !nearby {
		var err error
		scope, err = input.String("scope")
		if err != nil {
			return Result{}, err
		}
		if scope != "auto" && scope != "global" {
			return Result{}, invalidArgument("--scope must be auto or global", "choose auto or global")
		}
	}
	var results locationdto.Results
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		service, err := app.NewLocationService(locationprovider.NewFromEnvironment())
		if err != nil {
			return Result{}, locationCLIError(err)
		}
		var items []domain.Location
		if nearby {
			items, err = service.Nearby(ctx, *point, limit)
		} else {
			items, err = service.SearchScope(ctx, query, point, limit, scope)
		}
		if err != nil {
			return Result{}, locationCLIError(err)
		}
		results = locationdto.FromDomains(items)
	case ModeAPI:
		values := url.Values{}
		if !nearby {
			values.Set("scope", scope)
		}
		if query != "" {
			values.Set("q", query)
		}
		if point != nil {
			values.Set("latitude", strconv.FormatFloat(point.Latitude(), 'f', 7, 64))
			values.Set("longitude", strconv.FormatFloat(point.Longitude(), 'f', 7, 64))
		}
		if limit != 0 {
			values.Set("limit", strconv.Itoa(limit))
		}
		var err error
		meta, err = locationAPIRequest(ctx, invocation, endpoint, values, &results)
		if err != nil {
			return Result{}, locationCLIError(err)
		}
	default:
		return Result{}, errors.New("unsupported location transport")
	}
	if err := locationdto.ValidateResults(results); err != nil {
		return Result{}, invalidAPIResponse()
	}
	return Result{Data: results, Text: locationResultsText(results), Meta: meta}, nil
}

func optionalCLILocationPoint(input CommandInput) (*domain.GeoPoint, error) {
	latitudeChanged, longitudeChanged := input.Changed("latitude"), input.Changed("longitude")
	if !latitudeChanged && !longitudeChanged {
		return nil, nil
	}
	if latitudeChanged != longitudeChanged {
		return nil, invalidArgument("--latitude and --longitude must be supplied together", "provide both WGS84 values or neither")
	}
	return requiredCLILocationPoint(input)
}

func requiredCLILocationPoint(input CommandInput) (*domain.GeoPoint, error) {
	if !input.Changed("latitude") || !input.Changed("longitude") {
		return nil, invalidArgument("--latitude and --longitude are required", "provide both WGS84 decimal values")
	}
	latitude, err := input.Float64("latitude")
	if err != nil {
		return nil, err
	}
	longitude, err := input.Float64("longitude")
	if err != nil {
		return nil, err
	}
	point, err := domain.NewGeoPoint(latitude, longitude, domain.CoordinateWGS84)
	if err != nil {
		return nil, locationCLIError(err)
	}
	return point, nil
}

func locationAPIRequest(ctx context.Context, invocation Invocation, endpoint string, query url.Values, destination *locationdto.Results) (map[string]any, error) {
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
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil || decoder.Decode(&struct{}{}) != io.EOF || locationdto.ValidateResults(*destination) != nil {
		return nil, invalidAPIResponse()
	}
	return meta, nil
}

func locationResultsText(results locationdto.Results) string {
	if len(results.Items) == 0 {
		return "No locations found"
	}
	lines := make([]string, 0, len(results.Items))
	for index, item := range results.Items {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", index+1, item.Name, item.DisplayName))
	}
	return strings.Join(lines, "\n")
}

func locationCLIError(err error) error {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		exit := ExitInvalidInput
		if domainErr.Code == domain.CodeLocationProviderUnavailable {
			exit = ExitUnavailable
		}
		return NewCommandError(string(domainErr.Code), domainErr.Message, "check location inputs and provider configuration, then retry", exit).WithCause(err)
	}
	return err
}
