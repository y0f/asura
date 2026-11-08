package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"golang.org/x/net/proxy"

	"github.com/y0f/asura/v2/internal/model"
	"github.com/y0f/asura/v2/internal/safenet"
)

func validateMonitorProxy(m *model.Monitor) error {
	if m == nil {
		return errors.New("missing monitor configuration")
	}
	if m.ProxyURL == "" {
		if m.ProxyID != nil {
			return errors.New("the configured proxy is disabled or unavailable")
		}
		return nil
	}
	u, err := url.Parse(m.ProxyURL)
	if err != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid configured proxy endpoint")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("invalid configured proxy port")
		}
	} else if u.Scheme == "socks5" {
		return errors.New("the configured SOCKS5 proxy requires a port")
	}
	return model.ValidateProxyCompatibility(m, u.Scheme)
}

// ProxyDialer returns a DialContext function that routes through the given proxy URL.
// For SOCKS5 proxies, it uses golang.org/x/net/proxy.
// For HTTP proxies, it returns nil (callers should use http.Transport.Proxy instead).
// If proxyURL is empty, it returns nil.
//
// When allowPrivate is false the target address is validated before being
// forwarded to the proxy, because the proxy — not the local dialer — performs
// the final connection and would otherwise reach private/reserved IPs that the
// base dialer's safenet Control blocks for direct connections. The address is
// forwarded unchanged, hostname included; see validateProxyTarget for what
// the check does and does not cover.
func ProxyDialer(proxyURL string, baseDial func(ctx context.Context, network, addr string) (net.Conn, error), allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if proxyURL == "" {
		return nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}

	if u.Scheme != "socks5" {
		return nil
	}

	var auth *proxy.Auth
	if u.User != nil {
		auth = &proxy.Auth{User: u.User.Username()}
		if p, ok := u.User.Password(); ok {
			auth.Password = p
		}
	}

	dialer, err := proxy.SOCKS5("tcp", u.Host, auth, &contextDialer{dial: baseDial})
	if err != nil {
		return nil
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := validateProxyTarget(ctx, addr, allowPrivate); err != nil {
			return nil, err
		}
		if cd, ok := dialer.(proxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return dialer.Dial(network, addr)
	}
}

// validateProxyTarget rejects a proxied connection whose target host resolves to
// a private or reserved IP when allowPrivate is false, restoring the protection
// that safenet's dial Control provides for direct (non-proxied) connections.
// addr may be a bare host or host:port.
//
// The check resolves the host with Asura's resolver, but the proxy is still
// given the hostname and resolves it on its own, in its own network: that is
// the point of a proxy (other regions, split DNS, hostname ACLs). The check
// therefore protects the network Asura runs in from monitors aimed at it, but
// cannot constrain what the proxy itself reaches: a name may resolve
// differently for the proxy, or change between the two lookups. Restrict the
// proxy's reach with the proxy's own ACLs.
func validateProxyTarget(ctx context.Context, addr string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil {
		if safenet.IsPrivateIP(ip) {
			return fmt.Errorf("blocked: proxied target %s is a private/reserved IP", host)
		}
		return nil
	}
	ips, err := lookupProxyTarget(ctx, "ip", host)
	if err != nil {
		// Names only the proxy can resolve (split DNS) are not addresses in
		// Asura's network, which is all this check protects: let the proxy
		// resolve them, as v1.3.0 did.
		return nil
	}
	for _, ip := range ips {
		if safenet.IsPrivateIP(ip) {
			return fmt.Errorf("blocked: proxied target %s resolves to a private/reserved IP", host)
		}
	}
	return nil
}

// lookupProxyTarget resolves a proxied target host; a variable so tests can
// simulate DNS answers.
var lookupProxyTarget = net.DefaultResolver.LookupIP

// HTTPProxyURL returns a *url.URL for use with http.Transport.Proxy if the
// proxy URL uses HTTP. Returns nil for SOCKS5 or empty URLs.
func HTTPProxyURL(proxyURL string) *url.URL {
	if proxyURL == "" {
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u
	}
	return nil
}

// contextDialer wraps a DialContext func as a proxy.Dialer.
type contextDialer struct {
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (d *contextDialer) Dial(network, addr string) (net.Conn, error) {
	return d.dial(context.Background(), network, addr)
}

func (d *contextDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}
