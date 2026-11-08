package model

import (
	"strings"
	"testing"
)

func TestValidateMQTTSettings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings MQTTSettings
		valid    bool
	}{
		{"connection only", MQTTSettings{}, true},
		{"Unicode topic", MQTTSettings{Topic: "health/状態/+"}, true},
		{"binary password", MQTTSettings{Password: "\x00\xff"}, true},
		{"maximum string length", MQTTSettings{Topic: strings.Repeat("x", 65535)}, true},
		{"expected maximum", MQTTSettings{Topic: "health", ExpectMessage: strings.Repeat("x", 64<<10)}, true},
		{"expected needs topic", MQTTSettings{ExpectMessage: "private-expectation"}, false},
		{"expected too long", MQTTSettings{Topic: "health", ExpectMessage: strings.Repeat("x", (64<<10)+1)}, false},
		{"topic too long", MQTTSettings{Topic: strings.Repeat("x", 65536)}, false},
		{"username too long", MQTTSettings{Username: strings.Repeat("x", 65536)}, false},
		{"password too long", MQTTSettings{Password: strings.Repeat("x", 65536)}, false},
		{"client ID too long", MQTTSettings{ClientID: strings.Repeat("x", 65536)}, false},
		{"invalid UTF8 client ID", MQTTSettings{ClientID: "\xff"}, false},
		{"null username", MQTTSettings{Username: "private\x00username"}, false},
		{"null topic", MQTTSettings{Topic: "health\x00"}, false},
		{"embedded wildcard", MQTTSettings{Topic: "health+"}, false},
		{"nonterminal wildcard", MQTTSettings{Topic: "health/#/status"}, false},
		{"wildcard trailing slash", MQTTSettings{Topic: "health/#/"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMQTTSettings(tc.settings)
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v, valid=%v", err, tc.valid)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("validation echoed private input")
			}
		})
	}
}
