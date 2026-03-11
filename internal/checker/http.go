package checker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

const maxBodyRead = 1 << 20 // 1MB

var oauth2TokenCache sync.Map // map[int64]*oauth2CachedToken

type oauth2CachedToken struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type HTTPChecker struct {
	AllowPrivate bool
}

func (c *HTTPChecker) Type() string { return "http" }

func (c *HTTPChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.HTTPSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	method := settings.Method
	if method == "" {
		method = "GET"
	}

	target := monitor.Target
	if settings.CacheBuster {
		target = cacheBustURL(target)
	}

	var bodyReader io.Reader
	if settings.Body != "" {
		bodyReader = strings.NewReader(settings.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return &Result{Status: "down", Message: fmt.Sprintf("invalid request: %v", err)}, nil
	}

	applyBodyAndHeaders(req, settings)

	if settings.AuthMethod == "oauth2" {
		token, err := fetchOAuth2Token(ctx, monitor.ID, settings)
		if err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("oauth2 token fetch failed: %v", err)}, nil
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		applyAuthentication(req, settings)
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	baseDial := (&net.Dialer{
		Timeout: timeout,
		Control: safenet.MaybeDialControl(c.AllowPrivate),
	}).DialContext

	tlsCfg := &tls.Config{InsecureSkipVerify: settings.SkipTLSVerify}
	if settings.MTLSEnabled {
		if err := applyMTLS(tlsCfg, settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("mtls config failed: %v", err)}, nil
		}
	}

	transport := &http.Transport{
		DialContext:       baseDial,
		TLSClientConfig:   tlsCfg,
		DisableKeepAlives: true,
	}
	applyHTTPProxy(transport, monitor.ProxyURL, baseDial)

	client := &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: redirectPolicy(resolveMaxRedirects(settings)),
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		if ue, ok := err.(*url.Error); ok && ue.Err == http.ErrUseLastResponse {
			// not actually an error — we just don't follow redirects
		} else {
			return &Result{
				Status:       "down",
				ResponseTime: elapsed,
				Message:      fmt.Sprintf("request failed: %v", err),
			}, nil
		}
	}
	defer resp.Body.Close()

	return buildHTTPResult(resp, elapsed, settings)
}

func applyHTTPProxy(transport *http.Transport, proxyURL string, baseDial func(context.Context, string, string) (net.Conn, error)) {
	if proxyURL == "" {
		return
	}
	if socks := ProxyDialer(proxyURL, baseDial); socks != nil {
		transport.DialContext = socks
	} else if pu := HTTPProxyURL(proxyURL); pu != nil {
		transport.Proxy = http.ProxyURL(pu)
	}
}

func redirectPolicy(maxRedirects int) func(*http.Request, []*http.Request) error {
	if maxRedirects == 0 {
		return func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return func(_ *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
}

func buildHTTPResult(resp *http.Response, elapsed int64, settings storage.HTTPSettings) (*Result, error) {
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))

	h := sha256.Sum256(bodyBytes)
	headers := make(map[string]string)
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}

	status := "up"
	var msg string
	if settings.ExpectedStatus > 0 && resp.StatusCode != settings.ExpectedStatus {
		status = "down"
		msg = fmt.Sprintf("expected status %d, got %d", settings.ExpectedStatus, resp.StatusCode)
	}

	result := &Result{
		Status:       status,
		ResponseTime: elapsed,
		StatusCode:   resp.StatusCode,
		Message:      msg,
		Body:         string(bodyBytes),
		BodyHash:     hex.EncodeToString(h[:]),
		Headers:      headers,
	}

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		expiry := cert.NotAfter.Unix()
		result.CertExpiry = &expiry
		sum := sha256.Sum256(cert.Raw)
		result.CertFingerprint = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func cacheBustURL(target string) string {
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return target + sep + "_=" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func applyBodyAndHeaders(req *http.Request, settings storage.HTTPSettings) {
	if settings.Body != "" {
		switch settings.BodyEncoding {
		case "xml":
			req.Header.Set("Content-Type", "application/xml")
		case "form":
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		case "json":
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for k, v := range settings.Headers {
		req.Header.Set(k, v)
	}
}

func applyAuthentication(req *http.Request, settings storage.HTTPSettings) {
	switch settings.AuthMethod {
	case "basic":
		if settings.BasicAuthUser != "" {
			req.SetBasicAuth(settings.BasicAuthUser, settings.BasicAuthPass)
		}
	case "bearer":
		if settings.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+settings.BearerToken)
		}
	default:
		if settings.BasicAuthUser != "" {
			req.SetBasicAuth(settings.BasicAuthUser, settings.BasicAuthPass)
		}
	}
}

func fetchOAuth2Token(ctx context.Context, monitorID int64, settings storage.HTTPSettings) (string, error) {
	val, _ := oauth2TokenCache.LoadOrStore(monitorID, &oauth2CachedToken{})
	cached := val.(*oauth2CachedToken)

	cached.mu.Lock()
	defer cached.mu.Unlock()

	if cached.token != "" && time.Now().Before(cached.expiresAt.Add(-30*time.Second)) {
		return cached.token, nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {settings.OAuth2ClientID},
		"client_secret": {settings.OAuth2ClientSecret},
	}
	if settings.OAuth2Scopes != "" {
		data.Set("scope", settings.OAuth2Scopes)
	}
	if settings.OAuth2Audience != "" {
		data.Set("audience", settings.OAuth2Audience)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", settings.OAuth2TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response")
	}

	cached.token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		cached.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	} else {
		cached.expiresAt = time.Now().Add(time.Hour)
	}

	return cached.token, nil
}

func applyMTLS(tlsCfg *tls.Config, settings storage.HTTPSettings) error {
	cert, err := tls.X509KeyPair([]byte(settings.MTLSClientCert), []byte(settings.MTLSClientKey))
	if err != nil {
		return fmt.Errorf("parse client cert/key: %w", err)
	}
	tlsCfg.Certificates = []tls.Certificate{cert}

	if settings.MTLSCACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(settings.MTLSCACert)) {
			return fmt.Errorf("failed to parse CA certificate")
		}
		tlsCfg.RootCAs = pool
	}
	return nil
}

// ClearOAuth2TokenCache removes the cached token for a monitor (used when settings change).
func ClearOAuth2TokenCache(monitorID int64) {
	oauth2TokenCache.Delete(monitorID)
}

func resolveMaxRedirects(s storage.HTTPSettings) int {
	if s.MaxRedirects > 0 {
		return s.MaxRedirects
	}
	if s.FollowRedirects != nil {
		if !*s.FollowRedirects {
			return 0
		}
		return 10
	}
	return 10
}
