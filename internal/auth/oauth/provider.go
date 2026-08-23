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

// DeviceProvider is implemented by providers that can sign a person in on a
// machine with no browser to redirect back to.
//
// It is a second interface rather than two more methods on Provider because
// not every provider here can do it, and Google is the one in front of us:
// Google refuses the device endpoint to the web-application client this
// deployment registers, requiring a separately registered "TVs and Limited
// Input devices" client that has no redirect URI and is therefore not the
// client the browser flow uses. Widening Provider would put a method on the
// Google adapter that exists only to return an error, and a method nobody may
// call is one the compiler has stopped checking anything about.
type DeviceProvider interface {
	Provider
	// StartDevice asks the provider for a code a person types somewhere else.
	StartDevice(ctx context.Context) (DeviceAuthorization, error)
	// PollDevice reports whether that code has been approved yet. Every answer
	// that is not an identity is one of the sentinels below.
	PollDevice(ctx context.Context, deviceCode string) (app.ExternalIdentity, error)
}

// DeviceAuthorization is one started device sign-in: what a person has to be
// shown, and what whoever polls has to send back.
//
// The device code leaves this process and returns on every poll because nothing
// on this side remembers it. That is deliberate rather than a shortcut: this
// deployment has no ephemeral server-side store -- the browser flow keeps its
// own in-flight state in a signed cookie for exactly that reason -- and a
// device flow needs none as long as the caller carries the code.
type DeviceAuthorization struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	// Interval is the shortest gap the provider will tolerate between polls.
	Interval time.Duration
	// ExpiresIn is how long the user code stays typeable.
	ExpiresIn time.Duration
}

// A poll answers one of six ways and only one of them is an identity. The other
// five are sentinels rather than messages because a caller has to do something
// different for each: keep waiting, wait longer, start over, stop, or report
// that the code it was given was never real.
var (
	// ErrDevicePending means nobody has approved the code yet.
	ErrDevicePending = errors.New("device authorization is still pending")
	// ErrDeviceSlowDown means the caller is polling faster than allowed. It is
	// always carried by a *SlowDown, which knows the new interval.
	ErrDeviceSlowDown = errors.New("device authorization is being polled too often")
	// ErrDeviceExpired means the code aged out before anyone approved it.
	ErrDeviceExpired = errors.New("device authorization expired before it was approved")
	// ErrDeviceDenied means a person saw the code and said no.
	ErrDeviceDenied = errors.New("device authorization was refused")
	// ErrDeviceCodeInvalid means the provider never issued that code. It is a
	// caller mistake rather than a provider fault, which is the difference
	// between "try again in a moment" and "there is nothing here to wait for".
	ErrDeviceCodeInvalid = errors.New("device code is not one the provider issued")
)

// SlowDown is ErrDeviceSlowDown carrying the interval the provider now insists
// on. The number has to survive the return trip: a provider sends a new one
// with every slow_down, and a caller that keeps polling on the old one is asking
// to be rate-limited off the endpoint entirely.
type SlowDown struct {
	// Interval is what the provider asked for, or zero when it named none.
	Interval time.Duration
}

func (slow *SlowDown) Error() string {
	if slow.Interval <= 0 {
		return ErrDeviceSlowDown.Error()
	}
	return fmt.Sprintf("%s: wait %s", ErrDeviceSlowDown, slow.Interval)
}

func (slow *SlowDown) Unwrap() error { return ErrDeviceSlowDown }

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

// DeviceLookup finds a configured provider that can also sign a person in with
// no browser to redirect back to. A provider that is configured but cannot is
// reported as absent, because from a caller with no browser they are the same
// thing.
func (registry *Registry) DeviceLookup(name string) (DeviceProvider, bool) {
	provider, found := registry.Lookup(name)
	if !found {
		return nil, false
	}
	device, supported := provider.(DeviceProvider)
	return device, supported
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
