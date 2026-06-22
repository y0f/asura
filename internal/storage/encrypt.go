package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encPrefix = "enc:"

const (
	kdfIterations = 200000
	kdfKeyLen     = 32
)

// Encryptor performs AES-GCM encryption with a PBKDF2-derived key. A legacy
// SHA-256-derived key is retained so ciphertext written by older versions
// (bare sha256(key), no salt) can still be decrypted; values re-encrypt with
// the new key on their next write.
type Encryptor struct {
	gcm       cipher.AEAD
	legacyGCM cipher.AEAD
}

// NewEncryptor derives the encryption key from key using PBKDF2 with the
// supplied salt. The salt must be persisted (see Store.EnsureKDFSalt) so the
// same key derives the same encryption key across restarts. An empty key
// disables encryption (returns nil, nil).
func NewEncryptor(key string, salt []byte) (*Encryptor, error) {
	if key == "" {
		return nil, nil
	}
	derived, err := pbkdf2.Key(sha256.New, key, salt, kdfIterations, kdfKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	gcm, err := newGCM(derived)
	if err != nil {
		return nil, err
	}
	legacy := sha256.Sum256([]byte(key))
	legacyGCM, err := newGCM(legacy[:])
	if err != nil {
		return nil, err
	}
	return &Encryptor{gcm: gcm, legacyGCM: legacyGCM}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return gcm, nil
}

func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e == nil || plaintext == "" {
		return plaintext, nil
	}
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *Encryptor) Decrypt(data string) (string, error) {
	if e == nil || data == "" {
		return data, nil
	}
	if !strings.HasPrefix(data, encPrefix) {
		return data, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	nonceSize := e.gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	plaintext, err := e.gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		legacy, legacyErr := e.legacyGCM.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
		if legacyErr != nil {
			return "", fmt.Errorf("decrypt: %w", err)
		}
		return string(legacy), nil
	}
	return string(plaintext), nil
}
