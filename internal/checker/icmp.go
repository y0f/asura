package checker

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type ICMPChecker struct {
	AllowPrivate bool
}

func (c *ICMPChecker) Type() string { return "icmp" }

func (c *ICMPChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	timeout := time.Duration(monitor.Timeout) * time.Second
	start := time.Now()

	targets := resolveICMPTargets(ctx, monitor.Target)
	if len(targets) == 0 {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      "DNS resolution failed: no IPv4 or IPv6 address found",
		}, nil
	}

	var lastResult *Result
	for _, t := range targets {
		if ctx.Err() != nil {
			return &Result{
				Status:       "down",
				ResponseTime: time.Since(start).Milliseconds(),
				Message:      fmt.Sprintf("context cancelled: %v", ctx.Err()),
			}, nil
		}

		if !c.AllowPrivate && safenet.IsPrivateIP(t.ip) {
			lastResult = &Result{
				Status:       "down",
				ResponseTime: time.Since(start).Milliseconds(),
				Message:      fmt.Sprintf("blocked: connections to private/reserved IP %s are not allowed", t.ip),
			}
			continue
		}

		result := probeICMP(t.ip, t.isIPv6, start, timeout)
		if result.Status == "up" {
			return result, nil
		}
		lastResult = result
	}

	return lastResult, nil
}

type icmpTarget struct {
	ip     net.IP
	isIPv6 bool
}

func resolveICMPTargets(ctx context.Context, target string) []icmpTarget {
	var targets []icmpTarget
	if addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", target); err == nil {
		for _, a := range addrs {
			targets = append(targets, icmpTarget{ip: a, isIPv6: false})
		}
	}
	if addrs, err := net.DefaultResolver.LookupIP(ctx, "ip6", target); err == nil {
		for _, a := range addrs {
			targets = append(targets, icmpTarget{ip: a, isIPv6: true})
		}
	}
	return targets
}

func probeICMP(dst net.IP, isIPv6 bool, start time.Time, timeout time.Duration) *Result {
	conn, err := listenICMP(isIPv6)
	if err != nil {
		return &Result{Status: "down", ResponseTime: time.Since(start).Milliseconds(), Message: fmt.Sprintf("ICMP listen failed: %v", err)}
	}
	defer conn.Close()

	seq := int(icmpSeq.Add(1) & 0xffff)
	sent := time.Now()
	if err := sendEchoRequest(conn, dst, isIPv6, seq); err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      fmt.Sprintf("send failed: %v", err),
		}
	}

	return readEchoReply(conn, dst, sent, timeout, isIPv6, seq)
}

// icmpSeq gives every probe in the process its own sequence number. A raw
// socket receives every ICMP packet on the host, so concurrent monitors can
// only tell their replies apart by source, ID and sequence.
var icmpSeq atomic.Uint32

func listenICMP(isIPv6 bool) (*icmp.PacketConn, error) {
	if isIPv6 {
		conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
		if err != nil {
			conn, err = icmp.ListenPacket("udp6", "::")
		}
		return conn, err
	}
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		conn, err = icmp.ListenPacket("udp4", "0.0.0.0")
	}
	return conn, err
}

// isDatagram reports whether conn is an unprivileged ICMP datagram socket.
// Its LocalAddr is a *net.UDPAddr, whose Network() is "udp", never "udp4".
func isDatagram(conn *icmp.PacketConn) bool {
	_, ok := conn.LocalAddr().(*net.UDPAddr)
	return ok
}

func sendEchoRequest(conn *icmp.PacketConn, dst net.IP, isIPv6 bool, seq int) error {
	var msgType icmp.Type
	if isIPv6 {
		msgType = ipv6.ICMPTypeEchoRequest
	} else {
		msgType = ipv4.ICMPTypeEcho
	}

	msg := icmp.Message{
		Type: msgType,
		Code: 0,
		Body: &icmp.Echo{
			ID:   echoID,
			Seq:  seq,
			Data: []byte("asura-ping"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return err
	}

	var dstAddr net.Addr = &net.IPAddr{IP: dst}
	if isDatagram(conn) {
		dstAddr = &net.UDPAddr{IP: dst}
	}
	_, err = conn.WriteTo(wb, dstAddr)
	return err
}

var echoID = os.Getpid() & 0xffff

func readEchoReply(conn *icmp.PacketConn, dst net.IP, sent time.Time, timeout time.Duration, isIPv6 bool, seq int) *Result {
	conn.SetReadDeadline(sent.Add(timeout))
	proto := 1
	if isIPv6 {
		proto = 58
	}
	// The kernel rewrites the echo ID of datagram sockets and only delivers
	// that socket's own replies, so the ID is checked on raw sockets only.
	checkID := !isDatagram(conn)

	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: time.Since(sent).Milliseconds(),
				Message:      fmt.Sprintf("receive failed: %v", err),
			}
		}
		rm, err := icmp.ParseMessage(proto, rb[:n])
		if err != nil || !isEchoReplyFor(rm, peer, dst, seq, checkID) {
			continue
		}
		elapsed := time.Since(sent).Milliseconds()
		return &Result{
			Status:       "up",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("ping %s: %dms", dst, elapsed),
		}
	}
}

// isEchoReplyFor reports whether rm, received from peer, answers the echo
// request with sequence seq that was sent to dst.
func isEchoReplyFor(rm *icmp.Message, peer net.Addr, dst net.IP, seq int, checkID bool) bool {
	if rm.Type != ipv4.ICMPTypeEchoReply && rm.Type != ipv6.ICMPTypeEchoReply {
		return false
	}
	var from net.IP
	switch a := peer.(type) {
	case *net.IPAddr:
		from = a.IP
	case *net.UDPAddr:
		from = a.IP
	}
	if !from.Equal(dst) {
		return false
	}
	echo, ok := rm.Body.(*icmp.Echo)
	if !ok || echo.Seq != seq {
		return false
	}
	return !checkID || echo.ID == echoID
}
