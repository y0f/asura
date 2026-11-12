package safenet

import (
	"net"
	"testing"
)

// IPv6 translation addresses reach the IPv4 address they embed through a
// gateway, so a private embedded address must be blocked like the IPv4 one.
func TestIsPrivateIPEmbeddedIPv4(t *testing.T) {
	for _, tc := range []struct {
		ip      string
		private bool
	}{
		{"64:ff9b::7f00:1", true},    // NAT64 well-known prefix -> 127.0.0.1
		{"64:ff9b::a9fe:a9fe", true}, // NAT64 -> 169.254.169.254 (cloud metadata)
		{"64:ff9b::808:808", false},  // NAT64 -> 8.8.8.8
		{"64:ff9b:1:a00:1::", true},  // local-use NAT64 (RFC 8215) is internal-only
		{"64:ff9b:1::808:808", true},
		{"2002:c0a8:101::1", true},                     // 6to4 -> 192.168.1.1
		{"2002:808:808::1", false},                     // 6to4 -> 8.8.8.8
		{"2001:0:4136:e378:8000:63bf:80ff:fffe", true}, // Teredo client 127.0.0.1
		{"2001:0:808:808:8000:63bf:f7f7:f7f7", false},  // Teredo server and client 8.8.8.8
		{"::7f00:1", true},                             // IPv4-compatible 127.0.0.1
		{"::808:808", true},                            // any deprecated IPv4-compatible address
		{"::ffff:127.0.0.1", true},                     // IPv4-mapped
		{"::ffff:8.8.8.8", false},
		{"2001:db8::1", true},           // documentation
		{"3fff::1", true},               // documentation
		{"100::1", true},                // discard-only
		{"fec0::1", true},               // deprecated site-local
		{"2606:4700:4700::1111", false}, // public
		{"8.8.8.8", false},
		{"10.1.2.3", true},
	} {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test address %q", tc.ip)
		}
		if got := IsPrivateIP(ip); got != tc.private {
			t.Errorf("IsPrivateIP(%s) = %v, want %v", tc.ip, got, tc.private)
		}
		err := DialControl("tcp", net.JoinHostPort(tc.ip, "443"), nil)
		if (err != nil) != tc.private {
			t.Errorf("DialControl(%s) error = %v, want blocked=%v", tc.ip, err, tc.private)
		}
	}
	if !IsPrivateIP(nil) {
		t.Error("an invalid address must be treated as private")
	}
}
