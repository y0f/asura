package checker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type TLSChecker struct {
	AllowPrivate bool
}

func (c *TLSChecker) Type() string { return "tls" }

func (c *TLSChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.TLSSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	warnDays := settings.WarnDaysBefore
	if warnDays <= 0 {
		warnDays = 30
	}

	target := monitor.Target
	// Add default port if missing
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = target + ":443"
	}

	host, _, _ := net.SplitHostPort(target)

	baseDial := (&net.Dialer{
		Timeout: time.Duration(monitor.Timeout) * time.Second,
		Control: safenet.MaybeDialControl(c.AllowPrivate),
	}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	start := time.Now()
	rawConn, err := dialFn(ctx, "tcp", target)
	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      fmt.Sprintf("TLS connection failed: %v", err),
		}, nil
	}

	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: host})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return &Result{
			Status:       "down",
			ResponseTime: time.Since(start).Milliseconds(),
			Message:      fmt.Sprintf("TLS handshake failed: %v", err),
		}, nil
	}
	defer tlsConn.Close()
	elapsed := time.Since(start).Milliseconds()

	state := tlsConn.ConnectionState()

	if len(state.PeerCertificates) == 0 {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      "no certificates presented",
		}, nil
	}

	cert := state.PeerCertificates[0]
	expiry := cert.NotAfter
	expiryUnix := expiry.Unix()
	daysUntilExpiry := int(time.Until(expiry).Hours() / 24)
	sum := sha256.Sum256(cert.Raw)
	fingerprint := hex.EncodeToString(sum[:])

	result := &Result{
		Status:          "up",
		ResponseTime:    elapsed,
		CertExpiry:      &expiryUnix,
		CertFingerprint: fingerprint,
		Message:         fmt.Sprintf("cert expires in %d days (%s)", daysUntilExpiry, expiry.Format("2006-01-02")),
	}

	if daysUntilExpiry <= 0 {
		result.Status = "down"
		result.Message = fmt.Sprintf("certificate expired on %s", expiry.Format("2006-01-02"))
	} else if daysUntilExpiry <= warnDays {
		result.Status = "degraded"
		result.Message = fmt.Sprintf("cert expires in %d days (warning threshold: %d)", daysUntilExpiry, warnDays)
	}

	return result, nil
}
