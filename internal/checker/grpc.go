package checker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/model"
	"github.com/y0f/asura/v3/internal/safenet"
	"golang.org/x/net/http2"
)

type GRPCChecker struct {
	AllowPrivate bool
}

const maxGRPCResponseBytes = 4096

func (c *GRPCChecker) Type() string { return "grpc" }

func (c *GRPCChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	var settings model.GRPCSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	target := monitor.Target
	if _, _, err := net.SplitHostPort(target); err != nil {
		if settings.UseTLS {
			target += ":443"
		} else {
			target += ":50051"
		}
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	baseDial := (&net.Dialer{
		Timeout: timeout,
		Control: safenet.MaybeDialControl(c.AllowPrivate),
	}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	scheme, transport := grpcTransport(settings, target, dialFn)
	reqBody := encodeGRPCFrame(encodeHealthRequest(settings.ServiceName))

	url := fmt.Sprintf("%s://%s/grpc.health.v1.Health/Check", scheme, target)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return &Result{Status: "down", Message: fmt.Sprintf("invalid request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// Each check builds its own transport; release its pooled connection (and
	// readLoop goroutine) once the response body is closed.
	defer client.CloseIdleConnections()

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("gRPC request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGRPCResponseBytes+1))
	elapsed = time.Since(start).Milliseconds()
	if err != nil {
		return grpcFailure(resp.StatusCode, elapsed, "gRPC response read failed: %v", err), nil
	}
	if len(body) > maxGRPCResponseBytes {
		return grpcFailure(resp.StatusCode, elapsed, "gRPC response exceeds %d bytes", maxGRPCResponseBytes), nil
	}
	return evaluateGRPCResponse(resp, body, elapsed)
}

func grpcTransport(settings model.GRPCSettings, target string, dialFn func(context.Context, string, string) (net.Conn, error)) (string, http.RoundTripper) {
	if settings.UseTLS {
		host, _, _ := net.SplitHostPort(target)
		return "https", &http2.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         host,
				InsecureSkipVerify: settings.SkipTLSVerify,
			},
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				rawConn, err := dialFn(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				tlsConn := tls.Client(rawConn, cfg)
				if err := tlsConn.HandshakeContext(ctx); err != nil {
					rawConn.Close()
					return nil, err
				}
				return tlsConn, nil
			},
		}
	}
	return "http", &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialFn(ctx, network, addr)
		},
	}
}

func evaluateGRPCResponse(resp *http.Response, body []byte, elapsed int64) (*Result, error) {
	if resp.StatusCode != http.StatusOK {
		return grpcFailure(resp.StatusCode, elapsed, "gRPC HTTP status: %d", resp.StatusCode), nil
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/grpc" && mediaType != "application/grpc+proto") {
		return grpcFailure(resp.StatusCode, elapsed, "invalid gRPC content type: %q", resp.Header.Get("Content-Type")), nil
	}

	statusHeaders := resp.Trailer
	if len(resp.Header.Values("Grpc-Status")) > 0 {
		// Status in the initial headers is valid only for a trailers-only response.
		if len(body) != 0 || len(resp.Trailer.Values("Grpc-Status")) != 0 {
			return grpcFailure(resp.StatusCode, elapsed, "unexpected gRPC status in response headers"), nil
		}
		statusHeaders = resp.Header
	}
	statuses := statusHeaders.Values("Grpc-Status")
	if len(statuses) != 1 || statuses[0] == "" {
		return grpcFailure(resp.StatusCode, elapsed, "missing or repeated gRPC status"), nil
	}
	if statuses[0] != "0" {
		return grpcFailure(resp.StatusCode, elapsed, "gRPC error: status=%s message=%s", statuses[0], statusHeaders.Get("Grpc-Message")), nil
	}

	payload, err := decodeGRPCFrame(body)
	if err != nil {
		return grpcFailure(resp.StatusCode, elapsed, "invalid gRPC frame: %v", err), nil
	}
	status, err := decodeHealthResponse(payload)
	if err != nil {
		return grpcFailure(resp.StatusCode, elapsed, "invalid gRPC health response: %v", err), nil
	}
	return grpcHealthResult(status, resp.StatusCode, elapsed), nil
}

func grpcFailure(statusCode int, elapsed int64, format string, args ...any) *Result {
	return &Result{Status: "down", ResponseTime: elapsed, StatusCode: statusCode, Message: fmt.Sprintf(format, args...)}
}

func grpcHealthResult(status int32, statusCode int, elapsed int64) *Result {
	r := &Result{ResponseTime: elapsed, StatusCode: statusCode}
	switch status {
	case 1:
		r.Status, r.Message = "up", "gRPC health: SERVING"
	case 2:
		r.Status, r.Message = "down", "gRPC health: NOT_SERVING"
	case 3:
		r.Status, r.Message = "down", "gRPC health: SERVICE_UNKNOWN"
	default:
		r.Status = "down"
		r.Message = fmt.Sprintf("gRPC health: UNKNOWN(%d)", status)
	}
	return r
}

func encodeGRPCFrame(payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = 0 // no compression
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func decodeGRPCFrame(data []byte) ([]byte, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("frame too short: %d bytes", len(data))
	}
	if data[0] != 0 {
		return nil, fmt.Errorf("unsupported compression flag: %d", data[0])
	}
	length := binary.BigEndian.Uint32(data[1:5])
	if uint64(len(data)-5) != uint64(length) {
		return nil, fmt.Errorf("unary frame length: want %d, have %d", length, len(data)-5)
	}
	return data[5:], nil
}

func encodeHealthRequest(service string) []byte {
	if service == "" {
		return nil
	}
	serviceBytes := []byte(service)
	buf := make([]byte, 0, 2+len(serviceBytes))
	buf = append(buf, 0x0a) // field 1, wire type 2 (length-delimited)
	buf = binary.AppendUvarint(buf, uint64(len(serviceBytes)))
	buf = append(buf, serviceBytes...)
	return buf
}

func decodeHealthResponse(data []byte) (int32, error) {
	var status int32 // Omitted proto3 enum fields have their zero value (UNKNOWN).
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 || tag>>3 > 1<<29-1 {
			return 0, fmt.Errorf("invalid protobuf field tag")
		}
		data = data[n:]
		if tag>>3 == 1 {
			if tag&7 != 0 {
				return 0, fmt.Errorf("invalid health status wire type")
			}
			value, n := binary.Uvarint(data)
			if n <= 0 || int64(value) < -1<<31 || int64(value) > 1<<31-1 {
				return 0, fmt.Errorf("invalid health status varint")
			}
			status = int32(value) // For repeated singular fields, the last value wins.
			data = data[n:]
			continue
		}
		var err error
		data, err = skipProtoField(data, tag, 0)
		if err != nil {
			return 0, err
		}
	}
	return status, nil
}

func skipProtoField(data []byte, tag uint64, depth int) ([]byte, error) {
	var size uint64
	switch tag & 7 {
	case 0:
		_, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("invalid protobuf varint")
		}
		size = uint64(n)
	case 1:
		size = 8
	case 2:
		length, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("invalid protobuf length")
		}
		data, size = data[n:], length
	case 3:
		if depth >= 32 {
			return nil, fmt.Errorf("protobuf groups nested too deeply")
		}
		for len(data) > 0 {
			next, n := binary.Uvarint(data)
			if n <= 0 || next>>3 == 0 || next>>3 > 1<<29-1 {
				return nil, fmt.Errorf("invalid protobuf group tag")
			}
			data = data[n:]
			if next&7 == 4 {
				if next>>3 != tag>>3 {
					return nil, fmt.Errorf("mismatched protobuf end group")
				}
				return data, nil
			}
			var err error
			data, err = skipProtoField(data, next, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("unterminated protobuf group")
	case 5:
		size = 4
	default:
		return nil, fmt.Errorf("invalid protobuf wire type: %d", tag&7)
	}
	if size > uint64(len(data)) {
		return nil, fmt.Errorf("truncated protobuf field")
	}
	return data[size:], nil
}
