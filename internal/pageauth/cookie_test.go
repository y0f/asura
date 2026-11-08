package pageauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCookieIndependentHMACVector(t *testing.T) {
	// Independently derived with Python hmac.digest and urlsafe_b64encode.
	const want = "v1.1805068800.8TYyhUkF7FSjIJ5yIFrQKuJayXpf4YI_hRiZlLoKMR0"
	now := time.Unix(1805068800-int64(CookieLifetime/time.Second), 0)
	got, err := IssueCookie(42, strings.Repeat("a", 64), now)
	if err != nil || got != want || !VerifyCookie(want, 42, strings.Repeat("a", 64), now) {
		t.Fatalf("independent vector mismatch: cookie=%q error=%v", got, err)
	}
}

func TestCookieFixedExpiryAndAuthority(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for name, hash := range map[string]string{"legacy-password": strings.Repeat("a", 64), "salted-password": knownHash} {
		t.Run(name, func(t *testing.T) {
			value, err := IssueCookie(42, hash, now)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(value, hash) || len(value) > maxCookieBytes {
				t.Fatal("cookie exposes the password hash or exceeds its size limit")
			}
			for _, elapsed := range []time.Duration{0, time.Hour, CookieLifetime - time.Nanosecond} {
				if !VerifyCookie(value, 42, hash, now.Add(elapsed)) {
					t.Fatalf("valid cookie rejected at %v", elapsed)
				}
			}
			for _, elapsed := range []time.Duration{CookieLifetime, CookieLifetime + time.Second, 30 * 24 * time.Hour} {
				if VerifyCookie(value, 42, hash, now.Add(elapsed)) {
					t.Fatalf("expired cookie accepted at %v", elapsed)
				}
			}
			if VerifyCookie(value, 43, hash, now) || VerifyCookie(value, 42, strings.Repeat("b", 64), now) {
				t.Fatal("cookie survived a page or password-hash change")
			}
			if VerifyCookie(value, 42, hash, now.Add(-time.Second)) {
				t.Fatal("cookie with more than the fixed lifetime remaining was accepted")
			}
		})
	}
}

func TestCookieRejectsForgeryAndNoncanonicalEncoding(t *testing.T) {
	now := time.Unix(1800000000, 0)
	hash := strings.Repeat("a", 64)
	value, err := IssueCookie(42, hash, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(value, ".")
	legacy := sha256.Sum256([]byte(hash + ":sp-auth"))
	invalid := []string{
		"", "v1", "v1..", hex.EncodeToString(legacy[:]),
		strings.Replace(value, "v1.", "v2.", 1),
		value + ".extra", value + "=", value + "\n", value + "\r",
		strings.Repeat("x", maxCookieBytes+1),
		parts[0] + "." + parts[1] + "." + strings.Repeat("A", cookieMACSize),
		"v1." + strconv.FormatInt(now.Add(CookieLifetime+time.Second).Unix(), 10) + "." + parts[2],
		"v1." + parts[1] + "." + parts[2][:cookieMACSize-1] + "_", // Nonzero unused base64 bits.
	}
	// Even correctly signed alternate spellings or invalid expiry values must
	// fail the parser, rather than becoming additional accepted encodings.
	for _, expires := range []string{"0" + parts[1], "+" + parts[1], " " + parts[1], parts[1] + " ", "-1", "0", "9223372036854775808", "99999999999999999999"} {
		payload := cookiePrefix + expires
		invalid = append(invalid, payload+"."+base64.RawURLEncoding.EncodeToString(cookieMAC(42, hash, payload)))
	}
	for _, candidate := range invalid {
		if VerifyCookie(candidate, 42, hash, now) {
			t.Errorf("invalid cookie accepted: %q", candidate)
		}
	}
	// A valid MAC cannot extend the protocol's fixed lifetime either.
	payload := cookiePrefix + strconv.FormatInt(now.Add(CookieLifetime+time.Second).Unix(), 10)
	if VerifyCookie(payload+"."+base64.RawURLEncoding.EncodeToString(cookieMAC(42, hash, payload)), 42, hash, now) {
		t.Fatal("signed overlong lifetime accepted")
	}
}

func TestCookieRejectsUnprotectedOrInvalidAuthority(t *testing.T) {
	now := time.Unix(1800000000, 0)
	valid, err := IssueCookie(42, knownHash, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"", "not-a-password-hash", strings.Repeat("g", 64), strings.Repeat("x", 4096)} {
		if value, err := IssueCookie(42, hash, now); err == nil || value != "" || strings.Contains(err.Error(), hash) && hash != "" {
			t.Fatal("invalid hash was accepted or disclosed")
		}
		if VerifyCookie(valid, 42, hash, now) {
			t.Fatal("cookie accepted without a valid protected-page hash")
		}
	}
	for _, id := range []int64{0, -1} {
		if value, err := IssueCookie(id, knownHash, now); err == nil || value != "" || VerifyCookie(valid, id, knownHash, now) {
			t.Fatal("invalid page ID accepted")
		}
	}
	for _, invalidTime := range []time.Time{time.Time{}, time.Unix(0, 0), time.Unix(-1, 0)} {
		if value, err := IssueCookie(42, knownHash, invalidTime); err == nil || value != "" || VerifyCookie(valid, 42, knownHash, invalidTime) {
			t.Fatal("invalid clock accepted")
		}
	}
}
