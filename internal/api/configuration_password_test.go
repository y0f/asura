package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestConfigurationPasswordHashesRoundTripWithoutRehashing(t *testing.T) {
	for _, encoded := range []string{"", strings.Repeat("a", 64), "$asura-pbkdf2-sha256$v=1$i=600000$MDEyMzQ1Njc4OWFiY2RlZg$bEpkaq0Q0Get1ft52QeKFtqD1Q+BZwqOdZOySebZSTY"} {
		c := &storage.ConfigurationSnapshot{StatusPages: []*storage.StatusPage{{ID: 1, Slug: "page", Title: "Page", PasswordHash: encoded}}}
		body, err := json.Marshal(configurationFile(c, false))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeConfiguration(strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateImportedConfiguration(decoded); err != nil {
			t.Fatalf("supported password hash rejected: %v", err)
		}
		if got := decoded.StatusPages[0].PasswordHash; got != encoded {
			t.Fatal("import changed opaque password protection")
		}
	}
}

func TestConfigurationRejectsUnsupportedPasswordWorkAndFormat(t *testing.T) {
	for _, encoded := range []string{strings.Repeat("A", 64), "secret-invalid-hash", "$asura-pbkdf2-sha256$v=1$i=999999999999999999$MDEyMzQ1Njc4OWFiY2RlZg$bEpkaq0Q0Get1ft52QeKFtqD1Q+BZwqOdZOySebZSTY"} {
		c := &storage.ConfigurationSnapshot{StatusPages: []*storage.StatusPage{{ID: 1, Slug: "page", Title: "Page", PasswordHash: encoded}}}
		err := validateImportedConfiguration(c)
		if err == nil || strings.Contains(err.Error(), encoded) {
			t.Fatalf("unsupported password hash accepted or disclosed: %v", err)
		}
	}
}
