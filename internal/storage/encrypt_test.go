package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

var testSalt = []byte("0123456789abcdef")

func TestEncryptorRoundTrip(t *testing.T) {
	enc, err := NewEncryptor("test-secret-key", testSalt)
	if err != nil {
		t.Fatal(err)
	}

	original := `{"url":"https://hooks.slack.com/xxx","secret":"s3cr3t"}`
	encrypted, err := enc.Encrypt(original)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(encrypted, encPrefix) {
		t.Fatalf("expected enc: prefix, got %s", encrypted[:10])
	}
	if encrypted == original {
		t.Fatal("encrypted should differ from original")
	}

	decrypted, err := enc.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != original {
		t.Fatalf("expected %s, got %s", original, decrypted)
	}
}

func TestEncryptorNilPassthrough(t *testing.T) {
	var enc *Encryptor

	out, err := enc.Encrypt("hello")
	if err != nil || out != "hello" {
		t.Fatalf("nil encryptor should passthrough, got %q err=%v", out, err)
	}

	out, err = enc.Decrypt("hello")
	if err != nil || out != "hello" {
		t.Fatalf("nil encryptor should passthrough, got %q err=%v", out, err)
	}
}

func TestEncryptorEmptyString(t *testing.T) {
	enc, _ := NewEncryptor("key", testSalt)

	out, err := enc.Encrypt("")
	if err != nil || out != "" {
		t.Fatalf("empty string should passthrough, got %q err=%v", out, err)
	}

	out, err = enc.Decrypt("")
	if err != nil || out != "" {
		t.Fatalf("empty string should passthrough, got %q err=%v", out, err)
	}
}

func TestDecryptUnencryptedData(t *testing.T) {
	enc, _ := NewEncryptor("key", testSalt)

	plain := `{"url":"https://example.com"}`
	out, err := enc.Decrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if out != plain {
		t.Fatalf("unencrypted data should passthrough, got %s", out)
	}
}

func TestEncryptorDifferentNonces(t *testing.T) {
	enc, _ := NewEncryptor("key", testSalt)

	a, _ := enc.Encrypt("same data")
	b, _ := enc.Encrypt("same data")

	if a == b {
		t.Fatal("two encryptions of same data should produce different ciphertext")
	}

	da, _ := enc.Decrypt(a)
	db, _ := enc.Decrypt(b)
	if da != db || da != "same data" {
		t.Fatalf("both should decrypt to same value, got %q and %q", da, db)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	enc1, _ := NewEncryptor("key1", testSalt)
	enc2, _ := NewEncryptor("key2", testSalt)

	encrypted, _ := enc1.Encrypt("secret")
	_, err := enc2.Decrypt(encrypted)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestNewEncryptorEmptyKey(t *testing.T) {
	enc, err := NewEncryptor("", testSalt)
	if err != nil {
		t.Fatal(err)
	}
	if enc != nil {
		t.Fatal("expected nil encryptor for empty key")
	}
}

func TestDecryptLegacyCiphertext(t *testing.T) {
	const key = "legacy-key"
	legacy := legacyEncrypt(t, key, `{"token":"abc"}`)

	enc, err := NewEncryptor(key, testSalt)
	if err != nil {
		t.Fatal(err)
	}
	out, err := enc.Decrypt(legacy)
	if err != nil {
		t.Fatalf("expected legacy ciphertext to decrypt, got %v", err)
	}
	if out != `{"token":"abc"}` {
		t.Fatalf("unexpected plaintext %q", out)
	}
}

// legacyEncrypt reproduces the pre-PBKDF2 scheme: AES-GCM keyed by bare
// sha256(key) with no salt.
func legacyEncrypt(t *testing.T, key, plaintext string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct)
}
