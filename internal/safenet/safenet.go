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
		"fc00::/7",
		"fe80::/10",
		"ff00::/8", // multicast
	} {
		_, ipNet, _ := net.ParseCIDR(cidr)
		_privateRanges = append(_privateRanges, ipNet)
	}
}

// IsPrivateIP reports whether ip is in a private or reserved range.
func IsPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, r := range _privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
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
