package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// openConnCounter tracks how many server-side connections are currently open.
type openConnCounter struct{ open atomic.Int64 }

func (c *openConnCounter) hook(_ net.Conn, st http.ConnState) {
	switch st {
	case http.StateNew:
		c.open.Add(1)
	case http.StateClosed, http.StateHijacked:
		c.open.Add(-1)
	}
}

// countingListener counts accepted connections that have not been closed yet;
// it survives the h2c hijack, unlike ConnState.
type countingListener struct {
	net.Listener
	open *atomic.Int64
}

type countingConn struct {
	net.Conn
	once sync.Once
	open *atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.open.Add(1)
	return &countingConn{Conn: c, open: l.open}, nil
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.open.Add(-1) })
	return c.Conn.Close()
}

func settleGoroutines() int {
	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	return runtime.NumGoroutine()
}

// Regression: http_multi leaks a keep-alive transport per check.
func TestHTTPMultiReleasesConnections(t *testing.T) {
	var cc openConnCounter
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	srv.Config.ConnState = cc.hook
	srv.Start()
	defer srv.Close()

	settings, _ := json.Marshal(storage.MultiStepHTTPSettings{Steps: []storage.HTTPStep{{URL: srv.URL}}})
	mon := &storage.Monitor{Type: "http_multi", Timeout: 5, Settings: settings}
	c := &HTTPMultiChecker{AllowPrivate: true}

	before := settleGoroutines()
	const n = 50
	for i := 0; i < n; i++ {
		res, err := c.Check(context.Background(), mon)
		if err != nil || res.Status != "up" {
			t.Fatalf("precondition: check failed: %v %+v", err, res)
		}
	}
	after := settleGoroutines()
	open := cc.open.Load()
	t.Logf("%d checks: goroutines %d -> %d, server-side open conns %d", n, before, after, open)
	if open > 1 || after-before > 5 {
		t.Fatalf("http_multi leaked %d open connections and %d goroutines after %d checks", open, after-before, n)
	}
}

// Regression: gRPC checker leaks an HTTP/2 connection per check.
func TestGRPCReleasesConnections(t *testing.T) {
	var cc openConnCounter
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(200)
		w.Write(encodeGRPCFrame([]byte{0x08, 0x01}))
		w.Header().Set("Grpc-Status", "0")
	})
	srv := httptest.NewUnstartedServer(h2c.NewHandler(h, &http2.Server{}))
	srv.Listener = &countingListener{Listener: srv.Listener, open: &cc.open}
	srv.Start()
	defer srv.Close()

	mon := &storage.Monitor{Type: "grpc", Target: strings.TrimPrefix(srv.URL, "http://"), Timeout: 5}
	c := &GRPCChecker{AllowPrivate: true}

	before := settleGoroutines()
	const n = 50
	for i := 0; i < n; i++ {
		res, err := c.Check(context.Background(), mon)
		if err != nil || res.Status != "up" {
			t.Fatalf("precondition: check failed: %v %+v", err, res)
		}
	}
	after := settleGoroutines()
	open := cc.open.Load()
	t.Logf("%d checks: goroutines %d -> %d, server-side open conns %d", n, before, after, open)
	if open > 1 || after-before > 5 {
		t.Fatalf("gRPC checker leaked %d open connections and %d goroutines after %d checks", open, after-before, n)
	}
}

func publicIPv6(t *testing.T) net.IP {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip(err)
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP
		if ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			if l, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0")); err == nil {
				l.Close()
				return ip
			}
		}
	}
	t.Skip("no public (non-private) local address to host a fake proxy; safenet blocks loopback proxies")
	return nil
}

// Regression: HTTP proxy path follows redirects to private targets (SSRF).
func TestHTTPProxyBlocksRedirectToPrivate(t *testing.T) {
	ip := publicIPv6(t)
	l, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Skip(err)
	}
	publicTarget := "http://" + l.Addr().String() + "/"

	var mu sync.Mutex
	var seen []string
	proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		if strings.HasPrefix(r.URL.Host, "169.254.169.254") {
			w.Write([]byte("iam-role-secret"))
			return
		}
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})}
	go proxy.Serve(l)
	defer proxy.Close()

	mon := &storage.Monitor{Type: "http", Target: publicTarget, Timeout: 5, ProxyURL: "http://" + l.Addr().String()}
	res, err := (&HTTPChecker{AllowPrivate: false}).Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("proxy saw: %v; result status=%s code=%d body=%q msg=%q", seen, res.Status, res.StatusCode, res.Body, res.Message)
	for _, u := range seen {
		if strings.Contains(u, "169.254.169.254") {
			t.Fatalf("allow_private=false but redirect to private metadata URL was fetched through the proxy; body=%q", res.Body)
		}
	}
}

func shPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	return p
}

// Regression: command checker blocks past its context deadline when a grandchild holds the pipe.
func TestCommandHonoursDeadlineWithBackgroundChild(t *testing.T) {
	sh := shPath(t)
	settings, _ := json.Marshal(storage.CommandSettings{Command: sh, Args: []string{"-c", "sleep 6; echo hi"}})
	mon := &storage.Monitor{Type: "command", Timeout: 1, Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	res, _ := (&CommandChecker{Allowlist: []string{sh}}).Check(ctx, mon)
	el := time.Since(start)
	t.Logf("elapsed %v, result %+v", el, res)
	if el > 3*time.Second {
		t.Fatalf("command check with 1s deadline took %v (grandchild kept the pipe open)", el)
	}
}

// Regression: command checker buffers unbounded output into Body.
func TestCommandOutputIsCapped(t *testing.T) {
	sh := shPath(t)
	settings, _ := json.Marshal(storage.CommandSettings{Command: sh, Args: []string{"-c", "head -c 20000000 /dev/zero | tr '\\0' a"}})
	mon := &storage.Monitor{Type: "command", Timeout: 30, Settings: settings}
	res, _ := (&CommandChecker{Allowlist: []string{sh}}).Check(context.Background(), mon)
	t.Logf("status=%s body len=%d", res.Status, len(res.Body))
	if len(res.Body) > maxBodyRead {
		t.Fatalf("command output of %d bytes buffered and returned as Body (no cap; http caps at %d)", len(res.Body), maxBodyRead)
	}
}

// Regression: ICMP probe ignores the context deadline.
func TestICMPHonoursContextDeadline(t *testing.T) {
	if c, err := listenICMP(false); err != nil {
		t.Skipf("cannot open ICMP socket here: %v", err)
	} else {
		c.Close()
	}
	mon := &storage.Monitor{Type: "icmp", Target: "192.0.2.1", Timeout: 4} // TEST-NET-1, blackholed
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	res, _ := (&ICMPChecker{AllowPrivate: true}).Check(ctx, mon)
	el := time.Since(start)
	t.Logf("elapsed %v, result %+v", el, res)
	if el > 2*time.Second {
		t.Fatalf("ICMP check with a 1s context deadline ran for %v (per-probe deadline is sent+monitor.Timeout only)", el)
	}
}

// Regression: domain checker truncates multi-label public suffixes.
func TestSanitizeDomainMultiLabelSuffix(t *testing.T) {
	for _, d := range []string{"example.co.uk", "example.com.au", "example.com.br"} {
		if got := sanitizeDomain(d); got != d {
			t.Errorf("sanitizeDomain(%q) = %q, WHOIS would query the public suffix", d, got)
		}
	}
}

func TestProxiedRedirectPolicyBlocksPrivateTargets(t *testing.T) {
	policy := proxiedRedirectPolicy(redirectPolicy(10), false)
	for target, wantErr := range map[string]bool{
		"http://169.254.169.254/latest/meta-data/": true,
		"http://10.0.0.5/":                         true,
		"http://93.184.216.34/":                    false,
	} {
		req := httptest.NewRequest("GET", target, nil)
		if err := policy(req, nil); (err != nil) != wantErr {
			t.Errorf("redirect to %s: err=%v, want error=%v", target, err, wantErr)
		}
	}
}

func TestValidateProxyTarget(t *testing.T) {
	orig := lookupProxyTarget
	t.Cleanup(func() { lookupProxyTarget = orig })
	lookupProxyTarget = func(_ context.Context, _, host string) ([]net.IP, error) {
		switch host {
		case "internal.test":
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		case "proxy-only.corp":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}

	tests := []struct {
		addr         string
		allowPrivate bool
		wantErr      bool
	}{
		{"10.0.0.5:80", false, true},
		{"169.254.169.254", false, true},
		{"[::1]:443", false, true},
		{"internal.test:80", false, true},
		{"93.184.216.34:80", false, false},
		{"public.test:443", false, false},
		{"proxy-only.corp:443", false, false}, // split DNS: only the proxy resolves it
		{"10.0.0.5:80", true, false},
		{"internal.test:80", true, false},
	}
	for _, tt := range tests {
		if err := validateProxyTarget(context.Background(), tt.addr, tt.allowPrivate); (err != nil) != tt.wantErr {
			t.Errorf("validateProxyTarget(%q, allowPrivate=%v) = %v, want error=%v", tt.addr, tt.allowPrivate, err, tt.wantErr)
		}
	}
}

// The HTTP proxy, not the local dialer, connects to the target, so the target
// must be validated before the request is handed to the proxy.
func TestHTTPCheckValidatesTargetBeforeHTTPProxy(t *testing.T) {
	mon := &storage.Monitor{Type: "http", Target: "http://10.0.0.5/", Timeout: 2, ProxyURL: "http://192.0.2.1:8080"}
	res, err := (&HTTPChecker{AllowPrivate: false}).Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "down" || !strings.Contains(res.Message, "proxied target") {
		t.Fatalf("status=%s message=%q, want the private proxied target to be blocked", res.Status, res.Message)
	}
}

func TestSOCKSProxyDialerBlocksPrivateTarget(t *testing.T) {
	baseDial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("the proxy was dialled for a private target")
		return nil, net.ErrClosed
	}
	dial := ProxyDialer("socks5://192.0.2.1:1080", baseDial, false)
	if dial == nil {
		t.Fatal("ProxyDialer returned nil for a socks5 URL")
	}
	for _, addr := range []string{"10.0.0.5:80", "169.254.169.254:80"} {
		if _, err := dial(context.Background(), "tcp", addr); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Errorf("dial %s: err=%v, want blocked", addr, err)
		}
	}
}

// A SOCKS5 proxy is given the hostname, as in v1.3.0, so it resolves the
// target in its own network; the hostname is only validated locally first.
func TestSOCKSProxyDialerSendsHostname(t *testing.T) {
	orig := lookupProxyTarget
	t.Cleanup(func() { lookupProxyTarget = orig })
	lookupProxyTarget = func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}

	requested := make(chan string, 1)
	baseDial := func(context.Context, string, string) (net.Conn, error) {
		client, proxySide := net.Pipe()
		go func() {
			defer proxySide.Close()
			buf := make([]byte, 262)
			// Greeting: version, method count, methods; accept "no auth".
			if _, err := io.ReadFull(proxySide, buf[:2]); err != nil {
				return
			}
			if _, err := io.ReadFull(proxySide, buf[:buf[1]]); err != nil {
				return
			}
			proxySide.Write([]byte{5, 0})
			// Request: version, CONNECT, reserved, address type, address, port.
			if _, err := io.ReadFull(proxySide, buf[:4]); err != nil {
				return
			}
			var host string
			switch buf[3] {
			case 1:
				io.ReadFull(proxySide, buf[:4])
				host = net.IP(buf[:4]).String()
			case 4:
				io.ReadFull(proxySide, buf[:16])
				host = net.IP(buf[:16]).String()
			case 3:
				io.ReadFull(proxySide, buf[:1])
				n := int(buf[0])
				io.ReadFull(proxySide, buf[:n])
				host = string(buf[:n])
			}
			io.ReadFull(proxySide, buf[:2])
			requested <- net.JoinHostPort(host, strconv.Itoa(int(buf[0])<<8|int(buf[1])))
			proxySide.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		}()
		return client, nil
	}

	dial := ProxyDialer("socks5://192.0.2.1:1080", baseDial, false)
	conn, err := dial(context.Background(), "tcp", "public.test:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if got := <-requested; got != "public.test:80" {
		t.Fatalf("proxy was asked to connect to %q, want the hostname public.test:80", got)
	}
}

// An HTTP proxy is given the hostname, as in v1.3.0: the absolute URL for
// plain HTTP and a CONNECT to host:port for HTTPS.
func TestHTTPProxySendsHostname(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				mu.Lock()
				got = append(got, req.Method+" "+req.RequestURI)
				mu.Unlock()
				io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}()
		}
	}()

	baseDial := (&net.Dialer{}).DialContext
	transport := &http.Transport{DialContext: baseDial, DisableKeepAlives: true}
	applyHTTPProxy(transport, "http://"+ln.Addr().String(), baseDial, false)
	client := &http.Client{Transport: transport}
	if resp, err := client.Get("http://public.test/x"); err == nil {
		resp.Body.Close()
	}
	client.Get("https://public.test:8443/x") // the CONNECT is refused

	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET http://public.test/x", "CONNECT public.test:8443"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("proxy saw %q, want %q", got, want)
	}
}
