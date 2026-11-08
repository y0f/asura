package views

import (
	"slices"
	"strings"
)

func (p MonitorFormParams) settingsSecretPaths() []string {
	paths := make([]string, 0, len(p.SecretsStored))
	for key, stored := range p.SecretsStored {
		if stored && key != "target" && key != "assertions" {
			paths = append(paths, key)
		}
	}
	slices.Sort(paths)
	return paths
}

func secretPathIsURL(key string) bool {
	if key == "target" || key == "oauth2_token_url" {
		return true
	}
	parts := strings.SplitN(key, ".", 3)
	return len(parts) == 3 && parts[0] == "steps" && parts[2] == "url"
}
