package checker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/internal/storage"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestEncodeGRPCFrame(t *testing.T) {
	payload := []byte{0x0a, 0x03, 'f', 'o', 'o'}
	frame := encodeGRPCFrame(payload)

	if frame[0] != 0 {
		t.Errorf("compress flag = %d, want 0", frame[0])
	}

	if len(frame) != 10 {
		t.Errorf("frame length = %d, want 10", len(frame))
	}

	decoded, err := decodeGRPCFrame(frame)
	if err != nil {
		t.Fatalf("decodeGRPCFrame: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded = %v, want %v", decoded, payload)
	}
}

func TestDecodeGRPCFrameErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte{0, 0}},
		{"truncated", []byte{0, 0, 0, 0, 10}},
		{"truncated payload", []byte{0, 0, 0, 0, 2, 8}},
		{"trailing bytes", []byte{0, 0, 0, 0, 0, 8, 1}},
		{"multiple unary messages", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"compressed", []byte{1, 0, 0, 0, 0}},
		{"invalid compression flag", []byte{2, 0, 0, 0, 0}},
		{"maximum length", []byte{0, 255, 255, 255, 255}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeGRPCFrame(tt.data)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestEncodeHealthRequest(t *testing.T) {
	t.Run("empty service", func(t *testing.T) {
		result := encodeHealthRequest("")
		if result != nil {
			t.Errorf("expected nil for empty service, got %v", result)
		}
	})
	for _, size := range []int{127, 128, 255, 256, 16384} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			service := strings.Repeat("x", size)
			result := encodeHealthRequest(service)
			length, n := binary.Uvarint(result[1:])
			if result[0] != 0x0a || n <= 0 || length != uint64(size) || string(result[1+n:]) != service {
				t.Fatalf("invalid protobuf string encoding: length=%d bytes=%d size=%d", length, n, size)
			}
		})
	}

	t.Run("with service", func(t *testing.T) {
		result := encodeHealthRequest("myservice")
		if len(result) == 0 {
			t.Fatal("expected non-empty result")
		}
		if result[0] != 0x0a {
			t.Errorf("field tag = %02x, want 0x0a", result[0])
		}
		if result[1] != 9 {
			t.Errorf("string length = %d, want 9", result[1])
		}
		if string(result[2:]) != "myservice" {
			t.Errorf("service = %q, want %q", string(result[2:]), "myservice")
		}
	})
}

func TestDecodeHealthResponse(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int32
	}{
		{"empty = UNKNOWN", nil, 0},
		{"UNKNOWN", []byte{0x08, 0x00}, 0},
		{"SERVING", []byte{0x08, 0x01}, 1},
		{"NOT_SERVING", []byte{0x08, 0x02}, 2},
		{"SERVICE_UNKNOWN", []byte{0x08, 0x03}, 3},
		{"unknown enum", []byte{0x08, 0x81, 0x02}, 257},
		{"negative enum", append([]byte{8}, binary.AppendUvarint(nil, ^uint64(0))...), -1},
		{"last status wins", []byte{8, 1, 8, 2}, 2},
		{"unknown varint", []byte{0x10, 0x80, 0x01, 8, 1}, 1},
		{"unknown fixed64", []byte{0x11, 0, 0, 0, 0, 0, 0, 0, 0, 8, 1}, 1},
		{"unknown fixed32", []byte{0x15, 0, 0, 0, 0, 8, 1}, 1},
		{"unknown multibyte tag", []byte{0xe2, 0x12, 0, 8, 1}, 1},
		{"unknown group", []byte{0x13, 8, 1, 0x14, 8, 2}, 2},
		{"unknown nested group", []byte{0x13, 0x1b, 8, 2, 0x1c, 0x14, 8, 1}, 1},
		{"unknown long field", append(append([]byte{0x12, 0x80, 0x01}, make([]byte, 128)...), 8, 1), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeHealthResponse(tt.data)
			if err != nil || got != tt.want {
				t.Errorf("decodeHealthResponse() = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestDecodeHealthResponseMalformed(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"zero field", []byte{0}},
		{"truncated tag", []byte{0x80}},
		{"overflowing tag", bytes.Repeat([]byte{0xff}, 10)},
		{"field number out of range", binary.AppendUvarint(nil, 1<<32)},
		{"missing status", []byte{8}},
		{"truncated status", []byte{8, 0x80}},
		{"overflowing status", append([]byte{8}, bytes.Repeat([]byte{0xff}, 10)...)},
		{"status outside int32", append([]byte{8}, binary.AppendUvarint(nil, 1<<32+1)...)},
		{"wrong status wire type", []byte{0x0a, 0}},
		{"trailing malformed field after serving", []byte{8, 1, 0x10}},
		{"truncated unknown fixed64", []byte{0x11, 0}},
		{"truncated unknown fixed32", []byte{0x15, 0}},
		{"truncated length", []byte{0x12, 0x80}},
		{"oversized length", append([]byte{0x12}, binary.AppendUvarint(nil, ^uint64(0))...)},
		{"truncated unknown field", []byte{0x12, 2, 0}},
		{"invalid wire type", []byte{0x16}},
		{"unexpected end group", []byte{0x14}},
		{"mismatched end group", []byte{0x13, 0x1c}},
		{"unterminated group", []byte{0x13, 8, 1}},
		{"malformed group tag", []byte{0x13, 0}},
		{"nested too deeply", append(bytes.Repeat([]byte{0x13}, 33), bytes.Repeat([]byte{0x14}, 33)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status, err := decodeHealthResponse(tt.data); err == nil {
				t.Fatalf("accepted malformed message as status %d", status)
			}
		})
	}
}

func TestEvaluateGRPCResponse(t *testing.T) {
	serving := []byte{0, 0, 0, 0, 2, 8, 1}
	tests := []struct {
		name        string
		code        int
		contentType string
		statuses    []string
		header      bool
		body        []byte
		want        string
	}{
		{"serving", 200, "application/grpc", []string{"0"}, false, serving, "up"},
		{"protobuf media type", 200, "application/grpc+proto", []string{"0"}, false, serving, "up"},
		{"media type parameters", 200, "Application/GRPC; charset=utf-8", []string{"0"}, false, serving, "up"},
		{"omitted enum", 200, "application/grpc", []string{"0"}, false, []byte{0, 0, 0, 0, 0}, "down"},
		{"not serving", 200, "application/grpc", []string{"0"}, false, []byte{0, 0, 0, 0, 2, 8, 2}, "down"},
		{"HTTP error", 503, "application/grpc", []string{"0"}, false, serving, "down"},
		{"missing content type", 200, "", []string{"0"}, false, serving, "down"},
		{"non grpc content type", 200, "text/plain", []string{"0"}, false, serving, "down"},
		{"wrong serialization", 200, "application/grpc+json", []string{"0"}, false, serving, "down"},
		{"invalid content type prefix", 200, "application/grpc-invalid", []string{"0"}, false, serving, "down"},
		{"missing status", 200, "application/grpc", nil, false, serving, "down"},
		{"empty status", 200, "application/grpc", []string{""}, false, serving, "down"},
		{"error status", 200, "application/grpc", []string{"14"}, false, serving, "down"},
		{"repeated status", 200, "application/grpc", []string{"0", "14"}, false, serving, "down"},
		{"invalid status", 200, "application/grpc", []string{"00"}, false, serving, "down"},
		{"headers are not final status", 200, "application/grpc", []string{"0"}, true, serving, "down"},
		{"trailers only error", 200, "application/grpc", []string{"14"}, true, nil, "down"},
		{"trailers only lacks unary message", 200, "application/grpc", []string{"0"}, true, nil, "down"},
		{"malformed message after serving", 200, "application/grpc", []string{"0"}, false, []byte{0, 0, 0, 0, 3, 8, 1, 0}, "down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.code, Header: http.Header{}, Trailer: http.Header{}}
			resp.Header.Set("Content-Type", tt.contentType)
			statusHeaders := resp.Trailer
			if tt.header {
				statusHeaders = resp.Header
			}
			for _, status := range tt.statuses {
				statusHeaders.Add("Grpc-Status", status)
			}
			got, err := evaluateGRPCResponse(resp, tt.body, 42)
			if err != nil || got.Status != tt.want || got.StatusCode != tt.code || got.ResponseTime != 42 {
				t.Fatalf("response = %+v, %v; want status %s", got, err, tt.want)
			}
		})
	}
}

func TestGRPCCheckerResponseBody(t *testing.T) {
	serving := []byte{0, 0, 0, 0, 2, 8, 1}
	tests := []struct {
		name      string
		body      []byte
		fragment  bool
		shortBody bool
		stall     bool
		want      string
	}{
		{"fragmented serving frame", serving, true, false, false, "up"},
		{"unknown status", []byte{0, 0, 0, 0, 0}, true, false, false, "down"},
		{"truncated frame", serving[:6], false, false, false, "down"},
		{"incomplete HTTP body", serving, false, true, false, "down"},
		{"oversized response", make([]byte, maxGRPCResponseBytes+1), false, false, false, "down"},
		{"timeout before trailers", serving, false, false, true, "down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := strings.Repeat("x", 128)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, err := io.ReadAll(r.Body)
				if err != nil || len(request) < 8 || !bytes.Equal(request[5:8], []byte{0x0a, 0x80, 0x01}) || string(request[8:]) != service {
					t.Errorf("invalid health request: %x, %v", request, err)
				}
				w.Header().Set("Content-Type", "application/grpc")
				w.Header().Set("Trailer", "Grpc-Status")
				if tt.shortBody {
					w.Header().Set("Content-Length", strconv.Itoa(len(tt.body)+1))
				}
				if tt.fragment {
					for _, b := range tt.body {
						w.Write([]byte{b})
						w.(http.Flusher).Flush()
					}
				} else {
					w.Write(tt.body)
				}
				if tt.stall {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				w.Header().Set("Grpc-Status", "0")
			})
			srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
			defer srv.Close()
			settings, err := json.Marshal(storage.GRPCSettings{ServiceName: service})
			if err != nil {
				t.Fatal(err)
			}
			mon := &storage.Monitor{Target: strings.TrimPrefix(srv.URL, "http://"), Timeout: 1, Settings: settings}
			got, err := (&GRPCChecker{AllowPrivate: true}).Check(context.Background(), mon)
			if err != nil || got.Status != tt.want {
				t.Fatalf("check = %+v, %v; want %s", got, err, tt.want)
			}
			if (tt.shortBody || tt.stall) && !strings.Contains(got.Message, "read failed") {
				t.Fatalf("body error not reported: %+v", got)
			}
		})
	}
}

func TestGRPCCheckerRejectsRedirect(t *testing.T) {
	var redirected atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthy" {
			http.Redirect(w, r, "/healthy", http.StatusFound)
			return
		}
		redirected.Store(true)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{0, 0, 0, 0, 2, 8, 1})
		w.Header().Set("Grpc-Status", "0")
	})
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	defer srv.Close()
	mon := &storage.Monitor{Target: strings.TrimPrefix(srv.URL, "http://"), Timeout: 1}
	got, err := (&GRPCChecker{AllowPrivate: true}).Check(context.Background(), mon)
	if err != nil || got.Status != "down" || got.StatusCode != http.StatusFound || redirected.Load() {
		t.Fatalf("redirected=%v, check=%+v, error=%v", redirected.Load(), got, err)
	}
}

func TestGRPCFrameRoundTrip(t *testing.T) {
	empty := encodeGRPCFrame(nil)
	decoded, err := decodeGRPCFrame(empty)
	if err != nil {
		t.Fatalf("decode empty frame: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("decoded empty frame length = %d, want 0", len(decoded))
	}
}
