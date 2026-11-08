package pageauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// CookieLifetime is enforced by the server as well as the browser. Changing a
// page's password hash also invalidates all of its previously issued cookies.
const CookieLifetime = 7 * 24 * time.Hour

const (
	cookiePrefix   = "v1."
	cookieMACSize  = 43 // Unpadded URL-base64 SHA-256 MAC.
	maxCookieBytes = len(cookiePrefix) + 19 + 1 + cookieMACSize
)

// IssueCookie returns a page-bound credential with a fixed lifetime, rounded
// down to whole seconds. The current stored password hash is its signing key;
// neither the hash nor the plaintext password is included in the cookie.
func IssueCookie(pageID int64, passwordHash string, now time.Time) (string, error) {
	issued, expires := now.Unix(), now.Add(CookieLifetime).Unix()
	if pageID <= 0 || !ValidHash(passwordHash) || issued <= 0 || expires <= issued || expires-issued != int64(CookieLifetime/time.Second) {
		return "", errors.New("cannot issue status page cookie")
	}
	payload := cookiePrefix + strconv.FormatInt(expires, 10)
	return payload + "." + base64.RawURLEncoding.EncodeToString(cookieMAC(pageID, passwordHash, payload)), nil
}

// VerifyCookie rejects legacy unsigned cookies, noncanonical encodings,
// expired credentials and credentials for another page or password hash.
// Supplying now explicitly keeps expiry checks deterministic in callers/tests.
func VerifyCookie(value string, pageID int64, passwordHash string, now time.Time) bool {
	if len(value) > maxCookieBytes || !strings.HasPrefix(value, cookiePrefix) || pageID <= 0 {
		return false
	}
	expiresText, macText, ok := strings.Cut(value[len(cookiePrefix):], ".")
	if !ok || len(expiresText) == 0 || len(expiresText) > 19 || len(macText) != cookieMACSize {
		return false
	}
	expires, err := strconv.ParseInt(expiresText, 10, 64)
	current := now.Unix()
	if err != nil || strconv.FormatInt(expires, 10) != expiresText || current <= 0 || expires <= current || expires-current > int64(CookieLifetime/time.Second) {
		return false
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(macText)
	if err != nil || len(mac) != sha256.Size || !ValidHash(passwordHash) {
		return false
	}
	return hmac.Equal(mac, cookieMAC(pageID, passwordHash, cookiePrefix+expiresText))
}

func cookieMAC(pageID int64, passwordHash, payload string) []byte {
	mac := hmac.New(sha256.New, []byte(passwordHash))
	mac.Write([]byte("asura:status-page-cookie\x00"))
	mac.Write([]byte(strconv.FormatInt(pageID, 10)))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
