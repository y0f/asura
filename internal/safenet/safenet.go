package safenet

import (
	"fmt"
	"net"
	"syscall"
)

var _privateRanges []*net.IPNet

func init() {
	for _, cidr := range []string{
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"192.88.99.0/24",
		"192.168.0.0/16",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"224.0.0.0/4",
		"240.0.0.0/4",
		"255.255.255.255/32",
		"::/128", // unspecified; connect() to [::] reaches the local host like 0.0.0.0
		"::1/128",
		"::/96",          // deprecated IPv4-compatible addresses
		"64:ff9b:1::/48", // local-use NAT64 (RFC 8215), for internal networks only
		"100::/64",       // discard-only
		"2001:2::/48",    // benchmarking
		"2001:10::/28",   // ORCHID
		"2001:20::/28",   // ORCHIDv2
		"2001:db8::/32",  // documentation
		"3fff::/20",      // documentation
		"5f00::/16",      // SRv6 SIDs
		"fc00::/7",
		"fe80::/10",
		"fec0::/10", // deprecated site-local
		"ff00::/8",  // multicast
	} {
		_, ipNet, _ := net.ParseCIDR(cidr)
		_privateRanges = append(_privateRanges, ipNet)
	}
}

// Translation prefixes carry an IPv4 address inside an IPv6 one. A gateway on
// the path forwards them to that IPv4 address, so they are checked as IPv4.
var (
	_nat64Prefix  = mustCIDR("64:ff9b::/96")
	_6to4Prefix   = mustCIDR("2002::/16")
	_teredoPrefix = mustCIDR("2001::/32")
)

func mustCIDR(cidr string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return ipNet
}

// IsPrivateIP reports whether ip is in a private or reserved range, including
// IPv6 translation addresses (NAT64, 6to4, Teredo) whose embedded IPv4
// address is private or reserved.
func IsPrivateIP(ip net.IP) bool {
	if len(ip) != net.IPv4len && len(ip) != net.IPv6len {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, r := range _privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	if embedded := embeddedIPv4(ip); embedded != nil {
		return IsPrivateIP(embedded)
	}
	return false
}

// embeddedIPv4 returns the IPv4 address a translation mechanism would reach.
func embeddedIPv4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len {
		return nil
	}
	switch {
	case _nat64Prefix.Contains(ip):
		return net.IPv4(ip[12], ip[13], ip[14], ip[15])
	case _6to4Prefix.Contains(ip):
		return net.IPv4(ip[2], ip[3], ip[4], ip[5])
	case _teredoPrefix.Contains(ip):
		// Teredo stores the server at bytes 4-7 and the client XOR 0xff at 12-15.
		client := net.IPv4(ip[12]^0xff, ip[13]^0xff, ip[14]^0xff, ip[15]^0xff)
		if IsPrivateIP(client) {
			return client
		}
		return net.IPv4(ip[4], ip[5], ip[6], ip[7])
	}
	return nil
}

// DialControl is a net.Dialer Control function that blocks connections to
// private/reserved IP addresses. It is called after DNS resolution, before
// the connection is established.
func DialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked: invalid address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("blocked: could not parse IP %q", host)
	}
	if IsPrivateIP(ip) {
		return fmt.Errorf("blocked: connections to private/reserved IP %s are not allowed", ip)
	}
	return nil
}

// MaybeDialControl returns DialControl when allowPrivate is false,
// or nil (no restriction) when allowPrivate is true.
func MaybeDialControl(allowPrivate bool) func(string, string, syscall.RawConn) error {
	if allowPrivate {
		return nil
	}
	return DialControl
}
