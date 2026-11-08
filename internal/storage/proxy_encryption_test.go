package storage

import (
	"context"
	"strings"
	"testing"
)

func TestProxyReadsFailClosedOnUnreadablePasswords(t *testing.T) {
	for _, reason := range []string{"wrong key", "missing key", "malformed ciphertext"} {
		t.Run(reason, func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			gcm, err := newGCM([]byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			s.enc = &Encryptor{gcm: gcm, legacyGCM: gcm}
			p := &Proxy{Name: "protected", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "must-not-leak", Enabled: true}
			if err := s.CreateProxy(ctx, p); err != nil {
				t.Fatal(err)
			}
			var ciphertext string
			if err := s.readDB.QueryRow("SELECT auth_pass FROM proxies WHERE id=?", p.ID).Scan(&ciphertext); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "wrong key":
				wrong, err := newGCM([]byte("abcdef0123456789abcdef0123456789"))
				if err != nil {
					t.Fatal(err)
				}
				s.enc = &Encryptor{gcm: wrong, legacyGCM: wrong}
			case "missing key":
				s.enc = nil
			case "malformed ciphertext":
				ciphertext = "enc:invalid-secret-ciphertext"
				if _, err := s.writeDB.Exec("UPDATE proxies SET auth_pass=? WHERE id=?", ciphertext, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := s.GetProxy(ctx, p.ID); err == nil || got != nil || strings.Contains(err.Error(), ciphertext) || strings.Contains(err.Error(), p.AuthPass) {
				t.Fatalf("GetProxy returned credentials or exposed values: %v", err)
			}
			if got, err := s.ListProxies(ctx); err == nil || got != nil || strings.Contains(err.Error(), ciphertext) || strings.Contains(err.Error(), p.AuthPass) {
				t.Fatalf("ListProxies returned partial credentials or exposed values: %v", err)
			}
		})
	}
}

func TestProxyReadsRetainPlaintextAndValidEncryptedPasswords(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Proxy{Name: "legacy", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "plain-legacy-secret"}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetProxy(ctx, p.ID); err != nil || got.AuthPass != p.AuthPass {
		t.Fatalf("unencrypted legacy password lost: %v", err)
	}
	gcm, err := newGCM([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s.enc = &Encryptor{gcm: gcm, legacyGCM: gcm}
	if got, err := s.GetProxy(ctx, p.ID); err != nil || got.AuthPass != p.AuthPass {
		t.Fatalf("enabling encryption lost a legacy plaintext password: %v", err)
	}
	p.ID = 0
	p.Name = "encrypted"
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetProxy(ctx, p.ID); err != nil || got.AuthPass != p.AuthPass {
		t.Fatalf("encrypted password failed to decrypt: %v", err)
	}
	if got, err := s.ListProxies(ctx); err != nil || len(got) != 2 || got[0].AuthPass != p.AuthPass || got[1].AuthPass != p.AuthPass {
		t.Fatalf("valid proxy list changed credentials: %v", err)
	}
}
