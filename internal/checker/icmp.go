package checker

import (
	"context"
	"fmt"
	"net"
	"os"
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

	if err := sendEchoRequest(conn, dst, isIPv6); err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      fmt.Sprintf("send failed: %v", err),
		}
	}

	result, _ := readEchoReply(conn, dst, start, timeout, isIPv6)
	return result
}

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

func sendEchoRequest(conn *icmp.PacketConn, dst net.IP, isIPv6 bool) error {
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
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte("asura-ping"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return err
	}

	network := conn.LocalAddr().Network()
	var dstAddr net.Addr
	switch network {
	case "udp4":
		dstAddr = &net.UDPAddr{IP: dst}
	case "udp6":
		dstAddr = &net.UDPAddr{IP: dst}
	default:
		dstAddr = &net.IPAddr{IP: dst}
	}
	_, err = conn.WriteTo(wb, dstAddr)
	return err
}

func readEchoReply(conn *icmp.PacketConn, dst net.IP, start time.Time, timeout time.Duration, isIPv6 bool) (*Result, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	rb := make([]byte, 1500)
	n, _, err := conn.ReadFrom(rb)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("receive failed: %v", err),
		}, nil
	}

	var proto int
	network := conn.LocalAddr().Network()
	switch network {
	case "udp4":
		proto = 1
	case "udp6":
		proto = 58
	default:
		if isIPv6 {
			proto = 58
		} else {
			proto = 1
		}
	}

	rm, err := icmp.ParseMessage(proto, rb[:n])
	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("parse reply failed: %v", err),
		}, nil
	}

	if rm.Type == ipv4.ICMPTypeEchoReply || rm.Type == ipv6.ICMPTypeEchoReply {
		return &Result{
			Status:       "up",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("ping %s: %dms", dst, elapsed),
		}, nil
	}

	return &Result{
		Status:       "down",
		ResponseTime: elapsed,
		Message:      fmt.Sprintf("unexpected ICMP type: %v", rm.Type),
	}, nil
}
