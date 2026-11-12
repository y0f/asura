package checker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/model"
)

func mqttFixturePublish(topic, payload string) []byte {
	body := make([]byte, 2+len(topic)+len(payload))
	binary.BigEndian.PutUint16(body, uint16(len(topic)))
	copy(body[2:], topic)
	copy(body[2+len(topic):], payload)
	packet := append([]byte{0x30}, encodeRemainingLength(len(body))...)
	return append(packet, body...)
}

// Consume the real client's CONNECT or SUBSCRIBE without sharing its decoder.
func mqttFixtureRead(conn net.Conn) error {
	var b [1]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil {
		return err
	}
	length, scale := 0, 1
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			return err
		}
		length += int(b[0]&127) * scale
		if b[0]&128 == 0 {
			_, err := io.CopyN(io.Discard, conn, int64(length))
			return err
		}
		scale *= 128
	}
	return io.ErrUnexpectedEOF
}

func mqttMessageFixture(t *testing.T, settings model.MQTTSettings, replies ...[]byte) *Result {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if mqttFixtureRead(conn) != nil {
			return
		}
		if _, err := conn.Write([]byte{0x20, 2, 0, 0}); err != nil {
			return
		}
		if settings.Topic != "" {
			if mqttFixtureRead(conn) != nil {
				return
			}
			for _, reply := range replies {
				if _, err := conn.Write(reply); err != nil {
					return
				}
			}
		}
		io.Copy(io.Discard, conn)
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, err := (&MQTTChecker{AllowPrivate: true}).Check(ctx, &model.Monitor{
		Target: listener.Addr().String(), Timeout: 1, Settings: raw,
	})
	if err != nil || result == nil {
		t.Fatalf("Check = %v, %v", result, err)
	}
	return result
}

func TestMQTTExpectedMessageIsEnforced(t *testing.T) {
	ack := []byte{0x90, 3, 0, 1, 0}
	for _, tc := range []struct {
		name    string
		topic   string
		replies [][]byte
		wantUp  bool
	}{
		{"no publication", "health", [][]byte{ack}, false},
		{"wrong payload", "health", [][]byte{ack, mqttFixturePublish("health", "private-unhealthy")}, false},
		{"wrong topic", "health", [][]byte{ack, mqttFixturePublish("other", "private-ready")}, false},
		{"matching payload", "health", [][]byte{ack, mqttFixturePublish("health", "prefix-private-ready-suffix")}, true},
		{"matching before SUBACK", "health", [][]byte{mqttFixturePublish("health", "private-ready"), ack}, true},
		{"match still needs SUBACK", "health", [][]byte{mqttFixturePublish("health", "private-ready")}, false},
		{"mismatch then match", "health", [][]byte{ack, mqttFixturePublish("health", "pending"), mqttFixturePublish("health", "private-ready")}, true},
		{"topic required", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := mqttMessageFixture(t, model.MQTTSettings{Topic: tc.topic, ExpectMessage: "private-ready"}, tc.replies...)
			if (result.Status == "up") != tc.wantUp {
				t.Errorf("status=%s message=%s; want up=%v", result.Status, result.Message, tc.wantUp)
			}
			if strings.Contains(result.Message, "private-") {
				t.Errorf("result disclosed expected or received payload: %q", result.Message)
			}
		})
	}
}

func TestMQTTSubscriptionPacketBounds(t *testing.T) {
	ack := []byte{0x90, 3, 0, 1, 0}
	sized := func(size int, match bool) []byte {
		payload := strings.Repeat("x", size-2-len("health")-len("ready"))
		if match {
			payload += "ready"
		} else {
			payload += "other"
		}
		return mqttFixturePublish("health", payload)
	}
	exactTotal := [][]byte{ack}
	for range 3 {
		exactTotal = append(exactTotal, sized(256<<10, false))
	}
	// Count the fixed header, Remaining Length and SUBACK against the total.
	lastBodySize := (1 << 20) - len(ack) - 3*((256<<10)+4) - 4
	exactTotal = append(exactTotal, sized(lastBodySize, true))
	overTotal := append([][]byte(nil), exactTotal[:len(exactTotal)-1]...)
	overTotal = append(overTotal, sized(lastBodySize+1, true))
	exactCount := [][]byte{ack}
	for range 62 {
		exactCount = append(exactCount, mqttFixturePublish("health", "pending"))
	}
	exactCount = append(exactCount, mqttFixturePublish("health", "ready"))
	overCount := append([][]byte(nil), exactCount[:len(exactCount)-1]...)
	overCount = append(overCount, mqttFixturePublish("health", "pending"), mqttFixturePublish("health", "ready"))

	for _, tc := range []struct {
		name    string
		replies [][]byte
		wantUp  bool
		message string
	}{
		{"packet exact limit", [][]byte{ack, sized(256<<10, true)}, true, ""},
		{"packet over limit without body", [][]byte{ack, append([]byte{0x30}, encodeRemainingLength((256<<10)+1)...)}, false, "256 KiB"},
		{"total exact limit", exactTotal, true, ""},
		{"total over limit", overTotal, false, "1 MiB"},
		{"count exact limit", exactCount, true, ""},
		{"count over limit", overCount, false, "64 packets"},
		{"length overflow", [][]byte{ack, {0x30, 0x80, 0x80, 0x80, 0x80}}, false, "length encoding"},
		{"noncanonical length", [][]byte{ack, {0x30, 0x80, 0}}, false, "length encoding"},
		{"QoS1 despite requested QoS0", [][]byte{ack, {0x32, 3, 0, 1, 'x'}}, false, "QoS 0"},
		{"QoS0 duplicate flag", [][]byte{ack, {0x38, 3, 0, 1, 'x'}}, false, "QoS 0"},
		{"missing topic", [][]byte{ack, {0x30, 2, 0, 0}}, false, "topic length"},
		{"truncated topic", [][]byte{ack, {0x30, 3, 0, 2, 'x'}}, false, "topic length"},
		{"invalid UTF8 topic", [][]byte{ack, {0x30, 3, 0, 1, 0xff}}, false, "topic"},
		{"wildcard publication topic", [][]byte{ack, mqttFixturePublish("+", "ready")}, false, "topic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := mqttMessageFixture(t, model.MQTTSettings{Topic: "health", ExpectMessage: "ready"}, tc.replies...)
			if (result.Status == "up") != tc.wantUp || !strings.Contains(result.Message, tc.message) {
				t.Errorf("status=%s message=%s; want up=%v and message containing %q", result.Status, result.Message, tc.wantUp, tc.message)
			}
		})
	}
}

func TestMQTTSubscriptionModesAndTopicFilters(t *testing.T) {
	ack := []byte{0x90, 3, 0, 1, 0}
	for _, tc := range []struct {
		name, filter, topic, expected string
		beforeAck, retained, wantUp   bool
	}{
		{"connect only", "", "", "", false, false, true},
		{"subscribe only", "health", "", "", false, false, true},
		{"subscribe with early message", "health", "health", "", true, false, true},
		{"retained before ack", "health", "health", "ready", true, true, true},
		{"single level wildcard", "health/+", "health/node", "ready", false, false, true},
		{"empty wildcard level", "health/+", "health/", "ready", false, false, true},
		{"multi level wildcard", "health/#", "health/node/one", "ready", false, false, true},
		{"multi level zero levels", "health/#", "health", "ready", false, false, true},
		{"leading empty level", "/+", "/health", "ready", false, false, true},
		{"system explicit", "$SYS/#", "$SYS/health", "ready", false, false, true},
		{"system wildcard excluded", "#", "$SYS/health", "ready", false, false, false},
		{"topic case sensitive", "health", "HEALTH", "ready", false, false, false},
		{"payload case sensitive", "health", "health", "READY", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := [][]byte{ack}
			if tc.topic != "" {
				pub := mqttFixturePublish(tc.topic, "ready")
				if tc.retained {
					pub[0] |= 1
				}
				if tc.beforeAck {
					replies = [][]byte{pub, ack}
				} else {
					replies = append(replies, pub)
				}
			}
			result := mqttMessageFixture(t, model.MQTTSettings{Topic: tc.filter, ExpectMessage: tc.expected}, replies...)
			if (result.Status == "up") != tc.wantUp {
				t.Errorf("status=%s message=%s; want up=%v", result.Status, result.Message, tc.wantUp)
			}
		})
	}
}

func TestMQTTPasswordOnlyConnectHasUsernameFlag(t *testing.T) {
	packet := buildConnectPacket("client", "", "\x00\xff")
	// MQTT-3.1.2-22 requires the username flag whenever password is present.
	if packet[9] != 0xc2 || !bytes.Equal(packet[len(packet)-6:], []byte{0, 0, 0, 2, 0, 0xff}) {
		t.Fatal("password-only CONNECT must encode an empty username and binary password")
	}
}
