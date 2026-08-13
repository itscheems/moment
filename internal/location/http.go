package location

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const maxProviderResponse = 2 << 20

func getJSON(ctx context.Context, client HTTPClient, baseURL, endpoint string, query url.Values, destination any) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parse provider base URL: %w", err)
	}
	parsed.Path = endpoint
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), http.NoBody)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return ErrUnavailable
	}
	limited := io.LimitReader(response.Body, maxProviderResponse+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxProviderResponse {
		return ErrUnavailable
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return ErrUnavailable
	}
	return nil
}
