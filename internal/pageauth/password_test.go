package pageauth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Independently derived with Python hashlib.pbkdf2_hmac("sha256", password,
// b"0123456789abcdef", 600000, 32), encoded without base64 padding.
const knownHash = "$asura-pbkdf2-sha256$v=1$i=600000$MDEyMzQ1Njc4OWFiY2RlZg$bEpkaq0Q0Get1ft52QeKFtqD1Q+BZwqOdZOySebZSTY"

func TestVerifyIndependentPBKDF2Vector(t *testing.T) {
	if !ValidHash(knownHash) || !Verify(knownHash, "correct horse battery staple") {
		t.Fatal("independent PBKDF2 vector rejected")
	}
}

func TestHashUsesFreshSaltAndExactUnicodeByteLimit(t *testing.T) {
	password := strings.Repeat("é", MaxPasswordBytes/2)
	first, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !ValidHash(first) || !ValidHash(second) {
		t.Fatal("hashes must be valid and independently salted")
	}
	if !Verify(first, password) || Verify(first, password+"x") || Verify(first, strings.TrimSuffix(password, "é")) {
		t.Fatal("password boundary was truncated or compared incorrectly")
	}
}

func TestHashRejectsEmptyOrOversizedPasswordsWithoutDisclosure(t *testing.T) {
	for _, password := range []string{"", strings.Repeat("x", MaxPasswordBytes+1), strings.Repeat("é", MaxPasswordBytes/2+1)} {
		encoded, err := Hash(password)
		if encoded != "" || !errors.Is(err, ErrPasswordLength) {
			t.Fatalf("invalid length %d accepted: encoded=%q error=%v", len(password), encoded, err)
		}
		if password != "" && strings.Contains(err.Error(), password) {
			t.Fatal("password included in error")
		}
	}
}

func TestLegacySHA256Compatibility(t *testing.T) {
	for _, password := range []string{"old password", "", strings.Repeat("long legacy password", MaxPasswordBytes)} {
		digest := sha256.Sum256([]byte(password))
		encoded := hex.EncodeToString(digest[:])
		if !ValidHash(encoded) || !Verify(encoded, password) || Verify(encoded, password+"x") {
			t.Fatalf("legacy verification failed for length %d", len(password))
		}
		if ValidHash(strings.ToUpper(encoded)) || Verify(strings.ToUpper(encoded), password) {
			t.Fatal("noncanonical uppercase legacy digest accepted")
		}
	}
}

func TestRejectMalformedHashesBeforeDerivation(t *testing.T) {
	for _, encoded := range []string{
		"", "invalid", strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65),
		strings.Replace(knownHash, "v=1", "v=2", 1),
		strings.Replace(knownHash, "i=600000", "i=000001", 1),
		strings.Replace(knownHash, "i=600000", "i=999999", 1),
		strings.Replace(knownHash, "i=600000", "i=99999999999999999999", 1),
		strings.Replace(knownHash, "i=600000", "i=-60000", 1),
		strings.Replace(knownHash, "sha256", "sha512", 1),
		strings.Replace(knownHash, "Y2RlZg$", "Y2RlZh$", 1), // Nonzero base64 padding bits.
		strings.TrimSuffix(knownHash, "Y") + "Z",
		strings.Replace(knownHash, "Y2RlZg$", "Y2RlZ=$", 1),
		strings.Replace(knownHash, "MDEyMzQ1", "MDEy\nQ1", 1),
		knownHash + "$extra", knownHash + "=", " " + knownHash, knownHash + "\n",
		strings.Repeat("$", 1<<20),
	} {
		if ValidHash(encoded) || Verify(encoded, "correct horse battery staple") {
			t.Fatalf("malformed hash of length %d accepted", len(encoded))
		}
	}
}

func BenchmarkHash(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Hash("benchmark passphrase"); err != nil {
			b.Fatal(err)
		}
	}
}
