package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type UDPChecker struct {
	AllowPrivate bool
}

func (c *UDPChecker) Type() string { return "udp" }

func (c *UDPChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.UDPSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	timeout := time.Duration(monitor.Timeout) * time.Second

	if !c.AllowPrivate {
		host, _, _ := net.SplitHostPort(monitor.Target)
		if host == "" {
			host = monitor.Target
		}
		ips, err := net.LookupIP(host)
		if err == nil {
			for _, ip := range ips {
				if safenet.IsPrivateIP(ip) {
					return &Result{Status: "down", Message: "private IP targets not allowed"}, nil
				}
			}
		}
	}

	start := time.Now()
	conn, err := net.DialTimeout("udp", monitor.Target, timeout)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("dial failed: %v", err)}, nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	sendData := settings.SendData
	if sendData == "" {
		sendData = "\n"
	}

	if _, err := conn.Write([]byte(sendData)); err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("send failed: %v", err)}, nil
	}

	if settings.ExpectData != "" {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		elapsed = time.Since(start).Milliseconds()
		if err != nil {
			return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("no response: %v", err)}, nil
		}
		received := string(buf[:n])
		if !strings.Contains(received, settings.ExpectData) {
			return &Result{Status: "down", ResponseTime: elapsed, Message: "expected data not found"}, nil
		}
	}

	elapsed = time.Since(start).Milliseconds()
	return &Result{Status: "up", ResponseTime: elapsed, Message: "UDP OK"}, nil
}
