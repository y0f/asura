package checker

import (
	"net"
	"testing"
	"time"

	"github.com/y0f/asura/internal/model"
)

func TestMQTTRejectsInvalidAcknowledgements(t *testing.T) {
	for _, tc := range []struct {
		name      string
		subscribe bool
		reply     []byte
		wantOK    bool
	}{
		{"CONNACK success", false, []byte{0x20, 2, 0, 0}, true},
		{"CONNACK wrong flags", false, []byte{0x21, 2, 0, 0}, false},
		{"CONNACK wrong length", false, []byte{0x20, 3, 0, 0}, false},
		{"CONNACK length continuation", false, []byte{0x20, 0x80, 0, 0}, false},
		{"CONNACK stale session", false, []byte{0x20, 2, 1, 0}, false},
		{"CONNACK reserved session bits", false, []byte{0x20, 2, 2, 0}, false},
		{"CONNACK authentication refused", false, []byte{0x20, 2, 0, 5}, false},
		{"CONNACK truncated", false, []byte{0x20, 2, 0}, false},
		{"SUBACK success", true, []byte{0x90, 3, 0, 1, 0}, true},
		{"SUBACK wrong flags", true, []byte{0x91, 3, 0, 1, 0}, false},
		{"SUBACK wrong length", true, []byte{0x90, 4, 0, 1, 0}, false},
		{"SUBACK wrong identifier", true, []byte{0x90, 3, 0, 2, 0}, false},
		{"SUBACK zero identifier", true, []byte{0x90, 3, 0, 0, 0}, false},
		{"SUBACK excess QoS", true, []byte{0x90, 3, 0, 1, 1}, false},
		{"SUBACK reserved result", true, []byte{0x90, 3, 0, 1, 3}, false},
		{"SUBACK rejected", true, []byte{0x90, 3, 0, 1, 0x80}, false},
		{"SUBACK truncated", true, []byte{0x90, 3, 0, 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			client.SetDeadline(time.Now().Add(time.Second))
			server.SetDeadline(time.Now().Add(time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				// The fixture consumes the small CONNECT/SUBSCRIBE before replying.
				request := make([]byte, 512)
				if _, err := server.Read(request); err == nil {
					server.Write(tc.reply)
				}
			}()
			var err error
			if tc.subscribe {
				err = mqttSubscribe(client, "fixture/status")
			} else {
				err = mqttHandshake(client, "fixture", model.MQTTSettings{})
			}
			if (err == nil) != tc.wantOK {
				t.Errorf("malformed acknowledgement was accepted or valid reply rejected: %v", err)
			}
			client.Close()
			<-done
		})
	}
}
