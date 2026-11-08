package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateMQTTSettings also protects legacy settings and agent jobs before the
// checker encodes MQTT's two-byte string lengths. Passwords are binary data.
func ValidateMQTTSettings(s MQTTSettings) error {
	for _, field := range []struct{ name, value string }{
		{"client_id", s.ClientID}, {"username", s.Username}, {"topic", s.Topic},
	} {
		if len(field.value) > 65535 || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) {
			return fmt.Errorf("MQTT %s must be valid UTF-8 without null characters, at most 65535 bytes", field.name)
		}
	}
	if len(s.Password) > 65535 {
		return fmt.Errorf("MQTT password must be at most 65535 bytes")
	}
	if s.ExpectMessage != "" && s.Topic == "" {
		return fmt.Errorf("MQTT expected message requires a topic")
	}
	if len(s.ExpectMessage) > 64<<10 {
		return fmt.Errorf("MQTT expected message must be at most 64 KiB")
	}
	for rest := s.Topic; rest != ""; {
		level, next, more := strings.Cut(rest, "/")
		if strings.Contains(level, "#") && (level != "#" || more) || strings.Contains(level, "+") && level != "+" {
			return fmt.Errorf("MQTT topic wildcards must occupy a complete level; # must be last")
		}
		rest = next
	}
	return nil
}
