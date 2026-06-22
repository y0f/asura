package totp

import (
	"encoding/hex"
	"testing"
	"time"
)

// RFC 4226 Appendix D test vector secret
var rfc4226Secret = []byte("12345678901234567890")

func TestHOTP_RFC4226(t *testing.T) {
	expected := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}

	for i, want := range expected {
		got := hotp(rfc4226Secret, uint64(i))
		if got != want {
			t.Errorf("HOTP(counter=%d) = %s, want %s", i, got, want)
		}
	}
}

func TestCode_RFC6238(t *testing.T) {
	secret := rfc4226Secret

	tests := []struct {
		time int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}

	for _, tt := range tests {
		tm := time.Unix(tt.time, 0)
		got := Code(secret, tm)
		if got != tt.want {
			t.Errorf("Code(t=%d) = %s, want %s", tt.time, got, tt.want)
		}
	}
}

func TestValidate(t *testing.T) {
	secret := rfc4226Secret

	t.Run("exact time", func(t *testing.T) {
		tm := time.Unix(59, 0)
		code := Code(secret, tm)
		if !Validate(secret, code, tm) {
			t.Fatal("expected valid at exact time")
		}
	})

	t.Run("within skew", func(t *testing.T) {
		tm := time.Unix(59, 0)
		code := Code(secret, tm)
		// Should be valid ±30s
		if !Validate(secret, code, tm.Add(30*time.Second)) {
			t.Fatal("expected valid at +30s")
		}
		if !Validate(secret, code, tm.Add(-30*time.Second)) {
			t.Fatal("expected valid at -30s")
		}
	})

	t.Run("outside skew", func(t *testing.T) {
		tm := time.Unix(59, 0)
		code := Code(secret, tm)
		if Validate(secret, code, tm.Add(61*time.Second)) {
			t.Fatal("expected invalid at +61s")
		}
		if Validate(secret, code, tm.Add(-61*time.Second)) {
			t.Fatal("expected invalid at -61s")
		}
	})

	t.Run("wrong code", func(t *testing.T) {
		tm := time.Unix(59, 0)
		if Validate(secret, "000000", tm) {
			t.Fatal("expected wrong code to fail")
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		tm := time.Unix(59, 0)
		if Validate(secret, "12345", tm) {
			t.Fatal("expected short code to fail")
		}
		if Validate(secret, "1234567", tm) {
			t.Fatal("expected long code to fail")
		}
	})
}

func TestValidateWithCounter(t *testing.T) {
	secret := rfc4226Secret

	t.Run("returns matching counter", func(t *testing.T) {
		tm := time.Unix(90, 0)
		code := Code(secret, tm)
		counter, ok := ValidateWithCounter(secret, code, tm)
		if !ok {
			t.Fatal("expected valid code")
		}
		if counter != 3 {
			t.Fatalf("expected counter 3, got %d", counter)
		}
	})

	t.Run("skew returns origin counter", func(t *testing.T) {
		tm := time.Unix(90, 0)
		code := Code(secret, tm)
		counter, ok := ValidateWithCounter(secret, code, tm.Add(30*time.Second))
		if !ok {
			t.Fatal("expected valid code within skew")
		}
		if counter != 3 {
			t.Fatalf("expected counter 3 from prior step, got %d", counter)
		}
	})

	t.Run("invalid code", func(t *testing.T) {
		tm := time.Unix(90, 0)
		if _, ok := ValidateWithCounter(secret, "000000", tm); ok {
			t.Fatal("expected invalid code to fail")
		}
	})
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 20 {
		t.Fatalf("expected 20 bytes, got %d", len(s1))
	}

	s2, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(s1) == hex.EncodeToString(s2) {
		t.Fatal("two secrets should not be identical")
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	encoded := EncodeSecret(secret)
	decoded, err := DecodeSecret(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(secret) != hex.EncodeToString(decoded) {
		t.Fatal("roundtrip failed")
	}
}

func TestDecodeSecret_WithPadding(t *testing.T) {
	secret := []byte("12345678901234567890")
	encoded := EncodeSecret(secret)
	// Add padding manually
	padded := encoded + "==="
	decoded, err := DecodeSecret(padded)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(secret) != hex.EncodeToString(decoded) {
		t.Fatal("decode with padding failed")
	}
}

func TestFormatKeyURI(t *testing.T) {
	secret := rfc4226Secret
	uri := FormatKeyURI("Asura", "admin", secret)

	if uri == "" {
		t.Fatal("expected non-empty URI")
	}
	if len(uri) < 20 {
		t.Fatal("URI too short")
	}
	// Check required components
	want := "otpauth://totp/"
	if uri[:len(want)] != want {
		t.Fatalf("expected URI to start with %q, got %q", want, uri[:20])
	}
}
