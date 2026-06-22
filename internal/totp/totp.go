package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	secretSize = 20
	codeDigits = 6
	timeStep   = 30
)

// GenerateSecret returns 20 cryptographically random bytes suitable for TOTP.
func GenerateSecret() ([]byte, error) {
	b := make([]byte, secretSize)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate totp secret: %w", err)
	}
	return b, nil
}

// Code computes a 6-digit TOTP code for the given secret and time.
func Code(secret []byte, t time.Time) string {
	counter := uint64(t.Unix()) / timeStep
	return hotp(secret, counter)
}

// Validate checks a TOTP code against the secret with ±1 time step skew.
func Validate(secret []byte, code string, t time.Time) bool {
	_, ok := ValidateWithCounter(secret, code, t)
	return ok
}

// ValidateWithCounter checks a TOTP code against the secret with ±1 time step
// skew and reports which time-step counter matched. The returned counter
// enables replay protection: callers can reject a code whose counter is not
// strictly greater than the last accepted counter.
func ValidateWithCounter(secret []byte, code string, t time.Time) (uint64, bool) {
	if len(code) != codeDigits {
		return 0, false
	}
	counter := uint64(t.Unix()) / timeStep
	for _, offset := range []uint64{counter - 1, counter, counter + 1} {
		expected := hotp(secret, offset)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return offset, true
		}
	}
	return 0, false
}

// EncodeSecret returns the base32-encoded (no padding) string of a secret.
func EncodeSecret(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// DecodeSecret decodes a base32-encoded secret string.
func DecodeSecret(s string) ([]byte, error) {
	s = strings.TrimRight(strings.ToUpper(s), "=")
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}

// FormatKeyURI returns an otpauth:// URI for manual authenticator entry.
func FormatKeyURI(issuer, account string, secret []byte) string {
	encoded := EncodeSecret(secret)
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		label, encoded, url.QueryEscape(issuer), codeDigits, timeStep)
}

// hotp implements RFC 4226 HMAC-based One-Time Password with dynamic truncation.
func hotp(secret []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, secret)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	digits := int(code % uint32(math.Pow10(codeDigits)))
	return fmt.Sprintf("%0*d", codeDigits, digits)
}
