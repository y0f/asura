package checker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/y0f/asura/v3/internal/model"
	"github.com/y0f/asura/v3/internal/safenet"
)

type MQTTChecker struct {
	AllowPrivate bool
}

func (c *MQTTChecker) Type() string { return "mqtt" }

func (c *MQTTChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	if len(monitor.Settings) > 1<<20 {
		return &Result{Status: "down", Message: "MQTT settings exceed 1 MiB"}, nil
	}
	var settings model.MQTTSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}
	if err := model.ValidateMQTTSettings(settings); err != nil {
		return &Result{Status: "down", Message: err.Error()}, nil
	}

	target := monitor.Target
	if _, _, err := net.SplitHostPort(target); err != nil {
		if settings.UseTLS {
			target += ":8883"
		} else {
			target += ":1883"
		}
	}

	clientID := settings.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("asura-%d", rand.Int63())
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	baseDial := (&net.Dialer{Timeout: timeout, Control: safenet.MaybeDialControl(c.AllowPrivate)}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	start := time.Now()
	conn, err := dialFn(ctx, "tcp", target)
	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      fmt.Sprintf("MQTT connection failed: %v", err),
		}, nil
	}
	defer closeConnOnCancel(ctx, conn)()

	if settings.UseTLS {
		conn, err = mqttTLSUpgrade(ctx, conn, target)
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: time.Since(start).Milliseconds(),
				Message:      fmt.Sprintf("MQTT TLS handshake failed: %v", err),
			}, nil
		}
	}

	conn.SetDeadline(time.Now().Add(timeout))

	if err := mqttHandshake(conn, clientID, settings); err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      err.Error(),
		}, nil
	}

	if settings.Topic != "" {
		if err := mqttSubscribe(conn, settings.Topic, settings.ExpectMessage); err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: time.Since(start).Milliseconds(),
				Message:      err.Error(),
			}, nil
		}
	}

	conn.Write([]byte{0xe0, 0x00})

	return &Result{
		Status:       "up",
		ResponseTime: time.Since(start).Milliseconds(),
		Message:      "MQTT connection successful",
	}, nil
}

func mqttTLSUpgrade(ctx context.Context, conn net.Conn, target string) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(target)
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

func mqttHandshake(conn net.Conn, clientID string, settings model.MQTTSettings) error {
	if _, err := conn.Write(buildConnectPacket(clientID, settings.Username, settings.Password)); err != nil {
		return fmt.Errorf("MQTT CONNECT send failed: %w", err)
	}

	connackBuf := make([]byte, 4)
	if _, err := readFull(conn, connackBuf); err != nil {
		return fmt.Errorf("MQTT CONNACK read failed: %w", err)
	}

	// MQTT 3.1.1 CONNACK has no fixed-header flags, exactly two payload
	// bytes, and no stored session when our CONNECT requests a clean session.
	if connackBuf[0] != 0x20 || connackBuf[1] != 2 || connackBuf[2] != 0 {
		return fmt.Errorf("invalid MQTT CONNACK framing or session flags")
	}

	if code := connackBuf[3]; code != 0 {
		return fmt.Errorf("MQTT CONNACK rejected: code=%d (%s)", code, mqttConnackError(code))
	}
	return nil
}

const (
	mqttMaxPacketBytes         = 256 << 10
	mqttMaxSubscriptionBytes   = 1 << 20
	mqttMaxSubscriptionPackets = 64
)

func mqttSubscribe(conn net.Conn, topic, expected string) error {
	if _, err := conn.Write(buildSubscribePacket(1, topic)); err != nil {
		return fmt.Errorf("MQTT SUBSCRIBE send failed: %w", err)
	}

	budget := mqttMaxSubscriptionBytes
	acknowledged, matched := false, expected == ""
	needle := []byte(expected)
	for count := 0; count < mqttMaxSubscriptionPackets; count++ {
		header, body, err := mqttReadPacket(conn, &budget)
		if err != nil {
			if acknowledged && !matched {
				return fmt.Errorf("MQTT expected message not received: %w", err)
			}
			return fmt.Errorf("MQTT subscription failed: %w", err)
		}
		switch header {
		case 0x90:
			// One subscription, packet ID 1, maximum QoS 0.
			if acknowledged || len(body) != 3 || binary.BigEndian.Uint16(body[:2]) != 1 {
				return fmt.Errorf("invalid MQTT SUBACK framing or packet identifier")
			}
			if body[2] == 0x80 {
				return fmt.Errorf("MQTT subscription rejected")
			}
			if body[2] != 0 {
				return fmt.Errorf("invalid MQTT SUBACK return code for requested QoS 0")
			}
			acknowledged = true
		case 0x30, 0x31:
			// MQTT 3.1.1 permits PUBLISH before SUBACK. QoS 0 has no
			// packet identifier or DUP flag; RETAIN is allowed.
			if len(body) < 2 {
				return fmt.Errorf("invalid MQTT PUBLISH topic length")
			}
			n := int(binary.BigEndian.Uint16(body[:2]))
			if n == 0 || n > len(body)-2 {
				return fmt.Errorf("invalid MQTT PUBLISH topic length")
			}
			name := string(body[2 : 2+n])
			if !utf8.ValidString(name) || strings.ContainsAny(name, "\x00+#") || !mqttTopicMatches(topic, name) {
				return fmt.Errorf("invalid MQTT PUBLISH topic for subscription")
			}
			matched = matched || bytes.Contains(body[2+n:], needle)
		default:
			return fmt.Errorf("unexpected MQTT packet or flags for QoS 0 subscription")
		}
		if acknowledged && matched {
			return nil
		}
	}
	return fmt.Errorf("MQTT subscription exceeded 64 packets without completion")
}

func mqttReadPacket(conn net.Conn, budget *int) (byte, []byte, error) {
	var b [1]byte
	readByte := func() (byte, error) {
		if *budget <= 0 {
			return 0, fmt.Errorf("MQTT subscription exceeded 1 MiB")
		}
		*budget--
		_, err := io.ReadFull(conn, b[:])
		return b[0], err
	}
	header, err := readByte()
	if err != nil {
		return 0, nil, err
	}
	length := 0
	for i := 0; i < 4; i++ {
		value, err := readByte()
		if err != nil {
			return 0, nil, err
		}
		length |= int(value&127) << (7 * i)
		if value&128 != 0 {
			continue
		}
		if i > 0 && value == 0 {
			return 0, nil, fmt.Errorf("invalid MQTT remaining length encoding")
		}
		if length > mqttMaxPacketBytes {
			return 0, nil, fmt.Errorf("MQTT packet exceeded 256 KiB")
		}
		if length > *budget {
			return 0, nil, fmt.Errorf("MQTT subscription exceeded 1 MiB")
		}
		*budget -= length
		body := make([]byte, length)
		_, err = io.ReadFull(conn, body)
		return header, body, err
	}
	return 0, nil, fmt.Errorf("invalid MQTT remaining length encoding")
}

func mqttTopicMatches(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") && (strings.HasPrefix(filter, "+") || strings.HasPrefix(filter, "#")) {
		return false
	}
	for {
		if filter == "#" {
			return true
		}
		f, remainingFilter, moreFilter := strings.Cut(filter, "/")
		t, remainingTopic, moreTopic := strings.Cut(topic, "/")
		if f != "+" && f != t {
			return false
		}
		if !moreFilter {
			return !moreTopic
		}
		if !moreTopic {
			return remainingFilter == "#"
		}
		filter, topic = remainingFilter, remainingTopic
	}
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func encodeMQTTString(s string) []byte {
	b := make([]byte, 2+len(s))
	binary.BigEndian.PutUint16(b[0:2], uint16(len(s)))
	copy(b[2:], s)
	return b
}

func encodeRemainingLength(length int) []byte {
	var buf []byte
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		buf = append(buf, digit)
		if length == 0 {
			break
		}
	}
	return buf
}

func buildConnectPacket(clientID, username, password string) []byte {
	var payload []byte
	payload = append(payload, encodeMQTTString("MQTT")...)
	payload = append(payload, 4) // protocol level 4 (MQTT 3.1.1)

	var connectFlags byte
	connectFlags |= 0x02 // clean session

	if username != "" || password != "" {
		connectFlags |= 0x80
	}
	if password != "" {
		connectFlags |= 0x40
	}

	payload = append(payload, connectFlags)
	// The probe has a bounded connection deadline. Disable MQTT keepalive so
	// waiting for an expected message does not require a separate ping loop.
	payload = append(payload, 0, 0)
	payload = append(payload, encodeMQTTString(clientID)...)

	if username != "" || password != "" {
		payload = append(payload, encodeMQTTString(username)...)
	}
	if password != "" {
		payload = append(payload, encodeMQTTString(password)...)
	}

	var pkt []byte
	pkt = append(pkt, 0x10) // CONNECT packet type
	pkt = append(pkt, encodeRemainingLength(len(payload))...)
	pkt = append(pkt, payload...)
	return pkt
}

func buildSubscribePacket(packetID uint16, topic string) []byte {
	var payload []byte
	// packet identifier
	payload = append(payload, byte(packetID>>8), byte(packetID))
	payload = append(payload, encodeMQTTString(topic)...)
	payload = append(payload, 0) // QoS 0

	var pkt []byte
	pkt = append(pkt, 0x82) // SUBSCRIBE packet type with QoS 1
	pkt = append(pkt, encodeRemainingLength(len(payload))...)
	pkt = append(pkt, payload...)
	return pkt
}

func mqttConnackError(code byte) string {
	switch code {
	case 1:
		return "unacceptable protocol version"
	case 2:
		return "identifier rejected"
	case 3:
		return "server unavailable"
	case 4:
		return "bad username or password"
	case 5:
		return "not authorized"
	default:
		return "unknown"
	}
}
