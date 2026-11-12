package httputil

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractIP(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")

	tests := []struct {
		name        string
		remoteAddr  string
		cfIP        string
		xRealIP     string
		xff         string
		trustedNets []net.IPNet
		want        string
	}{
		{"direct untrusted", "1.2.3.4:1234", "", "", "", nil, "1.2.3.4"},
		{"trusted with X-Real-IP only", "127.0.0.1:1234", "", "10.0.0.1", "", []net.IPNet{*trusted}, "10.0.0.1"},
		{"trusted with XFF", "127.0.0.1:1234", "", "", "10.0.0.1, 127.0.0.1", []net.IPNet{*trusted}, "10.0.0.1"},
		{"untrusted ignores X-Real-IP", "1.2.3.4:1234", "", "10.0.0.1", "", []net.IPNet{*trusted}, "1.2.3.4"},
		{"proxy-appended XFF wins over passed-through X-Real-IP", "127.0.0.1:1234", "", "6.6.6.6", "192.168.1.1", []net.IPNet{*trusted}, "192.168.1.1"},
		{"no port in remote addr", "1.2.3.4", "", "", "", nil, "1.2.3.4"},
		{"client CF-Connecting-IP is ignored", "127.0.0.1:1234", "5.6.7.8", "", "192.168.1.1", []net.IPNet{*trusted}, "192.168.1.1"},
		{"client CF-Connecting-IP alone is ignored", "127.0.0.1:1234", "5.6.7.8", "", "", []net.IPNet{*trusted}, "127.0.0.1"},
		{"untrusted ignores CF-Connecting-IP", "1.2.3.4:1234", "5.6.7.8", "", "", []net.IPNet{*trusted}, "1.2.3.4"},
		{"spoofed left XFF entries are ignored", "127.0.0.1:1234", "", "", "9.9.9.9, 8.8.4.4, 203.0.113.7", []net.IPNet{*trusted}, "203.0.113.7"},
		{"garbage XFF cannot mint a new key", "127.0.0.1:1234", "", "", "random-123, 127.0.0.2", []net.IPNet{*trusted}, "127.0.0.2"},
		{"all-trusted chain keeps a valid address", "127.0.0.1:1234", "", "", "127.0.0.3, 127.0.0.2", []net.IPNet{*trusted}, "127.0.0.3"},
		{"XFF garbage only", "127.0.0.1:1234", "", "", "not-an-ip", []net.IPNet{*trusted}, "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.cfIP != "" {
				r.Header.Set("CF-Connecting-IP", tt.cfIP)
			}
			if tt.xRealIP != "" {
				r.Header.Set("X-Real-IP", tt.xRealIP)
			}
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := ExtractIP(r, tt.trustedNets)
			if got != tt.want {
				t.Errorf("ExtractIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTrusted(t *testing.T) {
	_, private, _ := net.ParseCIDR("10.0.0.0/8")
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	nets := []net.IPNet{*private, *loopback}

	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"127.0.0.1", true},
		{"192.168.1.1", false},
		{"8.8.8.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := IsTrusted(net.ParseIP(tt.ip), nets)
			if got != tt.want {
				t.Errorf("IsTrusted(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(10, 10)

	for i := 0; i < 10; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	if rl.Allow("1.2.3.4") {
		t.Fatal("11th request should be rate limited")
	}

	if !rl.Allow("5.6.7.8") {
		t.Fatal("different IP should be allowed")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	errWriter := func(w http.ResponseWriter, status int, msg string) {
		w.WriteHeader(status)
	}

	handler := rl.Middleware(nil, errWriter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", w.Code)
	}
}

func TestParsePagination(t *testing.T) {
	tests := []struct {
		query   string
		page    int
		perPage int
	}{
		{"", 1, 20},
		{"?page=2&per_page=50", 2, 50},
		{"?page=-1&per_page=200", 1, 20},
		{"?page=0&per_page=0", 1, 20},
		{"?page=abc", 1, 20},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/"+tt.query, nil)
			p := ParsePagination(r)
			if p.Page != tt.page {
				t.Errorf("page = %d, want %d", p.Page, tt.page)
			}
			if p.PerPage != tt.perPage {
				t.Errorf("perPage = %d, want %d", p.PerPage, tt.perPage)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	t.Run("valid id", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/monitors/42", nil)
		r.SetPathValue("id", "42")
		id, err := ParseID(r)
		if err != nil {
			t.Fatal(err)
		}
		if id != 42 {
			t.Errorf("got %d, want 42", id)
		}
	})

	t.Run("missing id", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/monitors/", nil)
		_, err := ParseID(r)
		if err == nil {
			t.Fatal("expected error for missing id")
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/monitors/abc", nil)
		r.SetPathValue("id", "abc")
		_, err := ParseID(r)
		if err == nil {
			t.Fatal("expected error for non-numeric id")
		}
	})

	t.Run("negative id", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/monitors/-1", nil)
		r.SetPathValue("id", "-1")
		_, err := ParseID(r)
		if err == nil {
			t.Fatal("expected error for negative id")
		}
	})
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	id2 := GenerateID()

	if len(id1) != 16 {
		t.Errorf("expected 16 hex chars, got %d", len(id1))
	}
	if id1 == id2 {
		t.Error("expected unique IDs")
	}
}

func TestStatusWriter(t *testing.T) {
	w := httptest.NewRecorder()
	sw := &StatusWriter{ResponseWriter: w}

	sw.WriteHeader(http.StatusNotFound)
	if sw.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404", sw.Code)
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("underlying Code = %d, want 404", w.Code)
	}
}
