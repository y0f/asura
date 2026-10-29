package checker

import (
	"net"
	"testing"

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

func TestICMPSeqUnique(t *testing.T) {
	a, b := icmpSeq.Add(1), icmpSeq.Add(1)
	if a == b {
		t.Fatalf("two probes got the same sequence %d", a)
	}
}
