package checker

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

func TestIsEchoReplyFor(t *testing.T) {
	dst := net.ParseIP("192.0.2.10")
	other := net.ParseIP("192.0.2.20")
	reply := func(typ icmp.Type, id, seq int) *icmp.Message {
		return &icmp.Message{Type: typ, Body: &icmp.Echo{ID: id, Seq: seq}}
	}

	tests := []struct {
		name    string
		msg     *icmp.Message
		peer    net.Addr
		checkID bool
		want    bool
	}{
		{"matching raw reply", reply(ipv4.ICMPTypeEchoReply, echoID, 7), &net.IPAddr{IP: dst}, true, true},
		{"matching datagram reply", reply(ipv4.ICMPTypeEchoReply, 4242, 7), &net.UDPAddr{IP: dst}, false, true},
		{"ipv6 reply from another host", reply(ipv6.ICMPTypeEchoReply, echoID, 7), &net.IPAddr{IP: net.ParseIP("2001:db8::1")}, true, false},
		{"reply from another monitor's target", reply(ipv4.ICMPTypeEchoReply, echoID, 7), &net.IPAddr{IP: other}, true, false},
		{"reply to another probe of the same target", reply(ipv4.ICMPTypeEchoReply, echoID, 8), &net.IPAddr{IP: dst}, true, false},
		{"reply to another process", reply(ipv4.ICMPTypeEchoReply, echoID^1, 7), &net.IPAddr{IP: dst}, true, false},
		{"datagram ignores rewritten id", reply(ipv4.ICMPTypeEchoReply, echoID^1, 7), &net.UDPAddr{IP: dst}, false, true},
		{"echo request seen on raw socket", reply(ipv4.ICMPTypeEcho, echoID, 7), &net.IPAddr{IP: dst}, true, false},
		{"destination unreachable", &icmp.Message{Type: ipv4.ICMPTypeDestinationUnreachable, Body: &icmp.DstUnreach{}}, &net.IPAddr{IP: dst}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEchoReplyFor(tt.msg, tt.peer, dst, 7, tt.checkID); got != tt.want {
				t.Errorf("isEchoReplyFor() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The unprivileged fallback is a datagram socket, which only accepts a
// *net.UDPAddr destination: writing an *net.IPAddr to it fails with EINVAL, so
// every probe on a host without raw-socket rights reported "send failed".
func TestICMPDatagramSocketProbe(t *testing.T) {
	conn, err := icmp.ListenPacket("udp4", "127.0.0.1")
	if err != nil {
		t.Skipf("unprivileged ICMP sockets not available: %v", err)
	}
	defer conn.Close()
	if !isDatagram(conn) {
		t.Fatalf("udp4 ICMP socket with local address %T not detected as datagram", conn.LocalAddr())
	}

	dst := net.ParseIP("127.0.0.1")
	seq := int(icmpSeq.Add(1) & 0xffff)
	sent := time.Now()
	if err := sendEchoRequest(conn, dst, false, seq); err != nil {
		t.Fatalf("send on datagram socket: %v", err)
	}
	if res := readEchoReply(conn, dst, sent, sent.Add(3*time.Second), false, seq); res.Status != "up" {
		t.Fatalf("no echo reply over the datagram socket: %s", res.Message)
	}
}
