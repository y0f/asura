package checker

import (
	"testing"
)

func TestEncodeMQTTString(t *testing.T) {
	tests := []struct {
		input    string
		wantLen  int
		wantHigh byte
		wantLow  byte
	}{
		{"", 2, 0, 0},
		{"MQTT", 6, 0, 4},
		{"test", 6, 0, 4},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := encodeMQTTString(tt.input)
			if len(result) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(result), tt.wantLen)
			}
			if result[0] != tt.wantHigh || result[1] != tt.wantLow {
				t.Errorf("length bytes = [%d, %d], want [%d, %d]", result[0], result[1], tt.wantHigh, tt.wantLow)
			}
			if tt.input != "" && string(result[2:]) != tt.input {
				t.Errorf("content = %q, want %q", string(result[2:]), tt.input)
			}
		})
	}
}

func TestEncodeRemainingLength(t *testing.T) {
	tests := []struct {
		input int
		want  []byte
	}{
		{0, []byte{0}},
		{127, []byte{127}},
		{128, []byte{0x80, 0x01}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 0x01}},
	}
	for _, tt := range tests {
		result := encodeRemainingLength(tt.input)
		if len(result) != len(tt.want) {
			t.Errorf("encodeRemainingLength(%d) = %v, want %v", tt.input, result, tt.want)
			continue
		}
		for i := range result {
			if result[i] != tt.want[i] {
				t.Errorf("encodeRemainingLength(%d) = %v, want %v", tt.input, result, tt.want)
				break
			}
		}
	}
}

func TestBuildConnectPacket(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		pkt := buildConnectPacket("test-client", "", "")
		if pkt[0] != 0x10 {
			t.Errorf("packet type = %02x, want 0x10", pkt[0])
		}
		// Verify MQTT protocol string is in the packet
		found := false
		for i := 0; i+3 < len(pkt); i++ {
			if pkt[i] == 'M' && pkt[i+1] == 'Q' && pkt[i+2] == 'T' && pkt[i+3] == 'T' {
				found = true
				break
			}
		}
		if !found {
			t.Error("MQTT protocol string not found in CONNECT packet")
		}
	})

	t.Run("with credentials", func(t *testing.T) {
		pkt := buildConnectPacket("client", "user", "pass")
		if pkt[0] != 0x10 {
			t.Errorf("packet type = %02x, want 0x10", pkt[0])
		}
		// Check connect flags include username and password
		// The flags byte is at a fixed offset in the variable header
		// After 0x10 + remaining length (1 byte for small packets) + "MQTT" string (2+4=6 bytes) + protocol level (1 byte)
		flagsOffset := 1 + 1 + 6 + 1 // type + remaining_len + mqtt_string + level
		if flagsOffset >= len(pkt) {
			t.Fatal("packet too short")
		}
		flags := pkt[flagsOffset]
		if flags&0x80 == 0 {
			t.Error("username flag not set")
		}
		if flags&0x40 == 0 {
			t.Error("password flag not set")
		}
		if flags&0x02 == 0 {
			t.Error("clean session flag not set")
		}
	})
}

func TestBuildSubscribePacket(t *testing.T) {
	pkt := buildSubscribePacket(1, "test/topic")
	if pkt[0] != 0x82 {
		t.Errorf("packet type = %02x, want 0x82", pkt[0])
	}
}

func TestMqttConnackError(t *testing.T) {
	tests := []struct {
		code byte
		want string
	}{
		{1, "unacceptable protocol version"},
		{2, "identifier rejected"},
		{3, "server unavailable"},
		{4, "bad username or password"},
		{5, "not authorized"},
		{6, "unknown"},
	}
	for _, tt := range tests {
		got := mqttConnackError(tt.code)
		if got != tt.want {
			t.Errorf("mqttConnackError(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}
