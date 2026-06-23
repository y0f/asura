package checker

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"golang.org/x/net/proxy"

	"github.com/y0f/asura/internal/safenet"
)

// ProxyDialer returns a DialContext function that routes through the given proxy URL.
// For SOCKS5 proxies, it uses golang.org/x/net/proxy.
// For HTTP proxies, it returns nil (callers should use http.Transport.Proxy instead).
// If proxyURL is empty, it returns nil.
//
// When allowPrivate is false the target address is validated before being
// forwarded to the proxy, because the proxy — not the local dialer — performs
// the final connection and would otherwise reach private/reserved IPs that the
// base dialer's safenet Control blocks for direct connections.
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
func validateProxyTarget(ctx context.Context, addr string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		if safenet.IsPrivateIP(ip) {
			return fmt.Errorf("blocked: proxied target %s is a private/reserved IP", host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve proxied target %s: %w", host, err)
	}
	for _, ip := range ips {
		if safenet.IsPrivateIP(ip) {
			return fmt.Errorf("blocked: proxied target %s resolves to a private/reserved IP", host)
		}
	}
	return nil
}

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
