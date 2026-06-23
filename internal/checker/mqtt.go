package checker

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type MQTTChecker struct {
	AllowPrivate bool
}

func (c *MQTTChecker) Type() string { return "mqtt" }

func (c *MQTTChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.MQTTSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
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
	defer conn.Close()

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
		if err := mqttSubscribe(conn, settings.Topic); err != nil {
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

func mqttHandshake(conn net.Conn, clientID string, settings storage.MQTTSettings) error {
	if _, err := conn.Write(buildConnectPacket(clientID, settings.Username, settings.Password)); err != nil {
		return fmt.Errorf("MQTT CONNECT send failed: %w", err)
	}

	connackBuf := make([]byte, 4)
	if _, err := readFull(conn, connackBuf); err != nil {
		return fmt.Errorf("MQTT CONNACK read failed: %w", err)
	}

	if connackBuf[0]>>4 != 2 {
		return fmt.Errorf("MQTT unexpected packet type: %d", connackBuf[0]>>4)
	}

	if code := connackBuf[3]; code != 0 {
		return fmt.Errorf("MQTT CONNACK rejected: code=%d (%s)", code, mqttConnackError(code))
	}
	return nil
}

func mqttSubscribe(conn net.Conn, topic string) error {
	if _, err := conn.Write(buildSubscribePacket(1, topic)); err != nil {
		return fmt.Errorf("MQTT SUBSCRIBE send failed: %w", err)
	}

	subackBuf := make([]byte, 5)
	if _, err := readFull(conn, subackBuf); err != nil {
		return fmt.Errorf("MQTT SUBACK read failed: %w", err)
	}

	if subackBuf[0]>>4 != 9 {
		return fmt.Errorf("MQTT unexpected packet type: %d (expected SUBACK)", subackBuf[0]>>4)
	}

	if subackBuf[4] == 0x80 {
		return fmt.Errorf("MQTT subscription rejected")
	}
	return nil
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

	if username != "" {
		connectFlags |= 0x80
	}
	if password != "" {
		connectFlags |= 0x40
	}

	payload = append(payload, connectFlags)
	payload = append(payload, 0, 30) // keepalive 30 seconds
	payload = append(payload, encodeMQTTString(clientID)...)

	if username != "" {
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

func decodeRemainingLength(data []byte) (int, int) {
	multiplier := 1
	value := 0
	for i, b := range data {
		value += int(b&0x7f) * multiplier
		multiplier *= 128
		if b&0x80 == 0 {
			return value, i + 1
		}
		if i >= 3 {
			break
		}
	}
	return value, len(data)
}
