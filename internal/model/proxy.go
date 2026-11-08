package model

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrUnsupportedProxy = errors.New("the selected proxy protocol is not supported by this monitor type")

// ValidateProxyCompatibility describes implemented transports, shared by
// configuration writes and runtime admission. It performs no network I/O.
func ValidateProxyCompatibility(m *Monitor, protocol string) error {
	if m == nil {
		return ErrUnsupportedProxy
	}
	if protocol == "http" || protocol == "https" {
		if m.Type == "http" || m.Type == "websocket" {
			return nil
		}
		return ErrUnsupportedProxy
	}
	if protocol != "socks5" {
		return ErrUnsupportedProxy
	}
	switch m.Type {
	case "http", "websocket", "tcp", "tls", "domain", "grpc", "mqtt":
		return nil
	case "dns":
		var settings DNSSettings
		if err := json.Unmarshal(m.Settings, &settings); err != nil || strings.TrimSpace(settings.Server) == "" {
			return errors.New("DNS checks through a proxy require an explicit DNS server")
		}
		return nil
	default:
		return ErrUnsupportedProxy
	}
}
