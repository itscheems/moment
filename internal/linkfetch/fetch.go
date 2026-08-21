// Package linkfetch owns every outbound request Moment makes on behalf of an
// owner-supplied URL.
//
// Safety lives in the transport, not at the call site. A recognizer or
// enricher receives a Fetcher, never an http.Client, and a Fetcher refuses to
// connect to a non-public address no matter which URL it is handed or how many
// redirects lead there. That placement is deliberate: an interface shaped like
// Do(*http.Request) would still let a caller name any host, so the guard has
// to sit where the socket is actually opened.
package linkfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/moment/moment/internal/domain"
)

// ErrBlockedAddress reports a refused connection to a non-public address.
var ErrBlockedAddress = errors.New("linkfetch: destination address is not publicly routable")

// ErrUnavailable reports any other failure to obtain usable bytes. Callers fold
// it into one stable public error code so a page's private failure mode is
// never mirrored back to the client.
var ErrUnavailable = errors.New("linkfetch: resource is unavailable")

const (
	// DefaultMaxRedirects bounds a redirect chain. Short-link expansion such as
	// b23.tv falls out of following these hops, and every hop is revalidated.
	DefaultMaxRedirects = 5
	// DefaultMaxDocumentBytes bounds an HTML or JSON metadata response.
	DefaultMaxDocumentBytes int64 = 2 << 20
	// DefaultMaxImageBytes bounds a thumbnail download. It is deliberately far
	// below the managed media ceiling because Moment chose to make this request.
	DefaultMaxImageBytes int64 = 8 << 20
	// DefaultTotalTimeout bounds one complete fetch including redirects.
	DefaultTotalTimeout = 10 * time.Second
	// DefaultDialTimeout bounds one TCP connection attempt.
	DefaultDialTimeout = 4 * time.Second
	// DefaultResponseHeaderTimeout bounds time to first byte on a slow origin.
	DefaultResponseHeaderTimeout = 6 * time.Second
	// UserAgent identifies Moment to origins without leaking instance details.
	UserAgent = "MomentLinkPreview/1.0 (+https://github.com/moment/moment)"
)

// blockedCIDRs names ranges that are routable in form but never a legitimate
// link-preview target. The address family helpers cover the rest.
//
// 198.18.0.0/15 is deliberately absent. It is the RFC 2544 benchmarking range
// and would otherwise belong here, but fake-IP DNS proxies -- the default mode
// of the tunnels commonly used to reach these platforms -- map real public
// hosts into exactly that range. Blocking it makes every unfurl fail on such a
// host while protecting nothing that the loopback, private, link-local, and
// metadata rules below do not already cover. Those higher-value rules stay.
var blockedCIDRs = []string{
	"0.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"::/128",
	"64:ff9b::/96",
	"2001:db8::/32",
}

var parsedBlockedCIDRs = func() []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(blockedCIDRs))
	for _, entry := range blockedCIDRs {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			panic("linkfetch: invalid blocked CIDR " + entry)
		}
		networks = append(networks, network)
	}
	return networks
}()

// Options configures a Fetcher. Every field is optional.
type Options struct {
	Transport             http.RoundTripper
	MaxRedirects          int
	TotalTimeout          time.Duration
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	// AllowPrivateAddresses disables destination filtering. It exists so tests
	// can target httptest servers on loopback and must never be set in
	// production wiring.
	AllowPrivateAddresses bool
}

// Fetcher performs bounded, address-filtered outbound requests.
type Fetcher struct {
	client       *http.Client
	maxRedirects int
}

// Response is the bounded result of one fetch.
type Response struct {
	FinalURL    string
	ContentType string
	Body        []byte
}

// New builds a Fetcher whose transport refuses non-public destinations.
func New(options Options) *Fetcher {
	maxRedirects := options.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}
	transport := options.Transport
	if transport == nil {
		dialer := &net.Dialer{
			Timeout:   durationOr(options.DialTimeout, DefaultDialTimeout),
			KeepAlive: 30 * time.Second,
			Control:   addressControl(options.AllowPrivateAddresses),
		}
		transport = &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   durationOr(options.DialTimeout, DefaultDialTimeout),
			ResponseHeaderTimeout: durationOr(options.ResponseHeaderTimeout, DefaultResponseHeaderTimeout),
			ExpectContinueTimeout: time.Second,
			MaxIdleConns:          8,
			IdleConnTimeout:       30 * time.Second,
			DisableCompression:    false,
		}
	}
	fetcher := &Fetcher{maxRedirects: maxRedirects}
	fetcher.client = &http.Client{
		Transport: transport,
		Timeout:   durationOr(options.TotalTimeout, DefaultTotalTimeout),
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w: redirect limit reached", ErrUnavailable)
			}
			if err := validateRequestURL(request.URL); err != nil {
				return err
			}
			// Redirects must not carry an owner's cookies or credentials onward.
			request.Header.Del("Cookie")
			request.Header.Del("Authorization")
			return nil
		},
		// A link fetch is anonymous by construction; never persist cookies.
		Jar: nil,
	}
	return fetcher
}

// Get fetches one URL, enforcing the scheme allowlist, the destination filter,
// the accepted media types, and a hard body ceiling.
func (fetcher *Fetcher) Get(ctx context.Context, rawURL string, accepted []string, maxBytes int64) (Response, error) {
	normalized, err := domain.NormalizeLinkURL(rawURL)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	target, err := url.Parse(normalized)
	if err != nil {
		return Response{}, fmt.Errorf("%w: unparseable url", ErrUnavailable)
	}
	if err := validateRequestURL(target); err != nil {
		return Response{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("User-Agent", UserAgent)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if len(accepted) > 0 {
		request.Header.Set("Accept", strings.Join(accepted, ", "))
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		if errors.Is(err, ErrBlockedAddress) {
			return Response{}, ErrBlockedAddress
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		_ = response.Body.Close()
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	contentType := baseContentType(response.Header.Get("Content-Type"))
	if len(accepted) > 0 && !acceptsContentType(accepted, contentType) {
		return Response{}, fmt.Errorf("%w: unsupported content type %q", ErrUnavailable, contentType)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDocumentBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if int64(len(body)) > maxBytes {
		return Response{}, fmt.Errorf("%w: response exceeds %d bytes", ErrUnavailable, maxBytes)
	}
	finalURL := normalized
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return Response{FinalURL: finalURL, ContentType: contentType, Body: body}, nil
}

func addressControl(allowPrivate bool) func(string, string, syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		if !strings.HasPrefix(network, "tcp") {
			return fmt.Errorf("%w: network %s", ErrBlockedAddress, network)
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBlockedAddress, err)
		}
		// Control runs against the address the socket will actually connect to,
		// so a name that resolved to a public IP during a first lookup cannot be
		// re-resolved to a private one before the connection is made.
		ip := net.ParseIP(host)
		if ip == nil || !isPublicAddress(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
		}
		return nil
	}
}

func isPublicAddress(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range parsedBlockedCIDRs {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func validateRequestURL(target *url.URL) error {
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrBlockedAddress, target.Scheme)
	}
	if target.User != nil {
		return fmt.Errorf("%w: credentials are not permitted", ErrBlockedAddress)
	}
	if target.Hostname() == "" {
		return fmt.Errorf("%w: missing host", ErrBlockedAddress)
	}
	return nil
}

func baseContentType(header string) string {
	if header == "" {
		return ""
	}
	parsed, _, err := mime.ParseMediaType(header)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	}
	return strings.ToLower(parsed)
}

func acceptsContentType(accepted []string, contentType string) bool {
	if contentType == "" {
		return false
	}
	for _, candidate := range accepted {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == contentType {
			return true
		}
		if strings.HasSuffix(candidate, "/*") && strings.HasPrefix(contentType, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
