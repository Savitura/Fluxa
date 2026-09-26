package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ErrUnsafeWebhookURL is returned when a webhook URL targets a scheme or
// network destination that could be used to reach internal infrastructure
// (loopback, link-local, RFC1918/ULA private ranges, multicast, or cloud
// metadata endpoints).
var ErrUnsafeWebhookURL = errors.New("webhook url is not allowed")

// allowedWebhookSchemes restricts registrations to plain HTTP(S) delivery,
// blocking schemes such as file://, gopher://, or javascript: that Go's
// http.Client would otherwise happily attempt to dereference.
var allowedWebhookSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// isBlockedIP reports whether ip must not be reachable from webhook
// deliveries: loopback, link-local (including the 169.254.169.254 cloud
// metadata address), RFC1918/ULA private ranges, multicast, and unspecified.
func (s *service) isBlockedIP(ip net.IP) bool {
	if s.allowPrivateNetworks {
		return false
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// resolveAndValidateHost resolves host to its IP addresses and rejects it if
// any resolved address falls into a disallowed range. It returns the first
// validated address so callers can pin the subsequent TCP connection to the
// exact address that was checked, closing the DNS-rebinding gap between
// validation and dial.
func (s *service) resolveAndValidateHost(ctx context.Context, host string) (net.IP, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: host %q resolved to no addresses", ErrUnsafeWebhookURL, host)
	}
	for _, ip := range ips {
		if s.isBlockedIP(ip) {
			return nil, fmt.Errorf("%w: host %q resolves to disallowed address %s", ErrUnsafeWebhookURL, host, ip)
		}
	}
	return ips[0], nil
}

// validateWebhookURL checks the URL scheme and, after resolving its host,
// the destination network. It is called both before persisting a webhook
// registration and immediately before each delivery attempt, since DNS
// records can change between registration time and send time.
func (s *service) validateWebhookURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeWebhookURL, err)
	}
	if !allowedWebhookSchemes[u.Scheme] {
		return fmt.Errorf("%w: scheme %q is not allowed", ErrUnsafeWebhookURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrUnsafeWebhookURL)
	}
	if _, err := s.resolveAndValidateHost(ctx, host); err != nil {
		return err
	}
	return nil
}

// ValidateWebhookURL is a package-level convenience that validates the URL
// scheme only. Full host resolution requires a service instance; callers
// needing SSRF protection should use s.validateWebhookURL instead.
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeWebhookURL, err)
	}
	if !allowedWebhookSchemes[u.Scheme] {
		return fmt.Errorf("%w: scheme %q is not allowed", ErrUnsafeWebhookURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: missing host", ErrUnsafeWebhookURL)
	}
	return nil
}

// newSafeHTTPClient builds the HTTP client used for webhook deliveries. It
// pins each connection to a freshly re-validated IP address (preventing
// DNS-rebinding between the pre-flight check and the actual dial) and
// disables automatic redirect following, since a redirect response could
// otherwise be used to smuggle a request to an internal address after the
// original URL passed validation.
func (s *service) newSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := s.resolveAndValidateHost(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Never follow redirects automatically; a redirect target has not
			// been through URL validation and could point at an internal
			// service. Callers see the redirect response itself instead.
			return http.ErrUseLastResponse
		},
	}
}
