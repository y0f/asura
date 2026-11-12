// Package pageauth hashes human passwords protecting public status pages.
// Random API and agent tokens use their separate token-hashing policies.
package pageauth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// MaxPasswordBytes bounds new password input without truncating Unicode.
	MaxPasswordBytes = 1024
	iterations       = 600000
	saltSize         = 16
	keySize          = 32
	prefix           = "$asura-pbkdf2-sha256$v=1$i=600000$"
	encodedSize      = len(prefix) + 22 + 1 + 43
)

var ErrPasswordLength = errors.New("status page password must contain between 1 and 1024 UTF-8 bytes")

// Hash produces a fresh salted PBKDF2-HMAC-SHA256 hash. An empty password is
// not a protected-page password; callers handle explicit protection removal.
func Hash(password string) (string, error) {
	if len(password) == 0 || len(password) > MaxPasswordBytes {
		return "", ErrPasswordLength
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("could not generate status page password salt")
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keySize)
	if err != nil {
		return "", errors.New("could not derive status page password hash")
	}
	return prefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// Verify accepts the current format and strict legacy lowercase SHA-256 hex.
// Legacy passwords are not truncated or subjected to the new-input size cap;
// HTTP callers retain their request body limits. Callers upgrade a verified
// legacy hash (see NeedsRehash).
func Verify(encoded, password string) bool {
	if legacy, ok := legacyDigest(encoded); ok {
		digest := sha256.Sum256([]byte(password))
		return subtle.ConstantTimeCompare(legacy, digest[:]) == 1
	}
	if len(password) == 0 || len(password) > MaxPasswordBytes {
		return false
	}
	salt, want, ok := parse(encoded)
	if !ok {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, keySize)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// NeedsRehash reports whether a verified hash uses the unsalted legacy SHA-256
// format and should be replaced by Hash of the same password.
func NeedsRehash(encoded string) bool {
	_, legacy := legacyDigest(encoded)
	return legacy
}

// ValidHash checks supported canonical encodings without deriving a key. The
// version and work factor are fixed, so imported data cannot choose huge work.
// An empty hash represents no protection and is handled by the caller.
func ValidHash(encoded string) bool {
	if _, ok := legacyDigest(encoded); ok {
		return true
	}
	_, _, ok := parse(encoded)
	return ok
}

func legacyDigest(encoded string) ([]byte, bool) {
	if len(encoded) != sha256.Size*2 {
		return nil, false
	}
	for _, c := range encoded {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, false
		}
	}
	digest, err := hex.DecodeString(encoded)
	return digest, err == nil
}

func parse(encoded string) (salt, key []byte, ok bool) {
	if len(encoded) != encodedSize || !strings.HasPrefix(encoded, prefix) {
		return nil, nil, false
	}
	saltText, keyText, found := strings.Cut(encoded[len(prefix):], "$")
	if !found || len(saltText) != 22 || len(keyText) != 43 {
		return nil, nil, false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(saltText)
	if err != nil || len(salt) != saltSize {
		return nil, nil, false
	}
	key, err = base64.RawStdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != keySize {
		return nil, nil, false
	}
	return salt, key, true
}
