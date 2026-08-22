// Package oauth turns a provider's answer about a person into the one shape
// the account layer accepts.
//
// Everything provider-specific stops at this boundary. Google speaks OpenID
// Connect and hands back a signed token; GitHub speaks plain OAuth 2.0 and
// needs two further calls to find an address. Neither difference is visible
// above an Identity, which is what keeps a third provider from touching
// sign-in, account creation, linking, or the browser.
package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/moment/moment/internal/app"
)

// ErrUnavailable is returned when a provider cannot be reached or answers in a
// way that cannot be used. Callers report one stable code rather than mirroring
// a remote failure mode back to a browser.
var ErrUnavailable = errors.New("identity provider is unavailable")

// Provider exchanges an authorization code for a verified external identity.
type Provider interface {
	// Name is the stable key stored in identities.provider.
	Name() string
	// Label is what the sign-in button says.
	Label() string
	// AuthCodeURL builds the address the browser is sent to.
	AuthCodeURL(state, verifier, nonce string) string
	// Exchange completes the flow and reports who signed in.
	Exchange(ctx context.Context, code, verifier, nonce string) (app.ExternalIdentity, error)
}

// Credentials are one provider's registered client.
type Credentials struct {
	ClientID     string
	ClientSecret string
}

// Configured reports whether both halves are present. A half-configured
// provider is treated as absent rather than as broken, matching how the
// location providers already handle missing keys.
func (credentials Credentials) Configured() bool {
	return strings.TrimSpace(credentials.ClientID) != "" && strings.TrimSpace(credentials.ClientSecret) != ""
}

// Registry holds the providers this deployment can actually use.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry builds a registry from whichever providers were configured.
func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		registry.providers[provider.Name()] = provider
	}
	return registry
}

// Lookup finds a configured provider by name.
func (registry *Registry) Lookup(name string) (Provider, bool) {
	if registry == nil {
		return nil, false
	}
	provider, found := registry.providers[strings.ToLower(strings.TrimSpace(name))]
	return provider, found
}

// Names lists the configured providers in a stable order.
func (registry *Registry) Names() []string {
	if registry == nil {
		return nil
	}
	names := make([]string, 0, len(registry.providers))
	for name := range registry.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Empty reports whether nothing is configured.
func (registry *Registry) Empty() bool { return registry == nil || len(registry.providers) == 0 }

// Describe reports each configured provider's name and label.
func (registry *Registry) Describe() []Descriptor {
	names := registry.Names()
	described := make([]Descriptor, 0, len(names))
	for _, name := range names {
		described = append(described, Descriptor{Name: name, Label: registry.providers[name].Label()})
	}
	return described
}

// Descriptor is what a sign-in page needs to draw a button.
type Descriptor struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// httpClient is the outbound client every provider shares.
//
// Provider endpoints are fixed, well-known hosts rather than addresses a user
// supplies, so the SSRF-hardened fetcher the link previewer uses would be the
// wrong tool here -- it exposes only GET, and a token exchange is a POST. What
// does matter is that a hung provider cannot hold a sign-in open indefinitely.
func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// withClient binds the shared client to a context for x/oauth2.
func withClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, httpClient())
}

func unavailable(reason string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, reason)
	}
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, reason, err)
}
