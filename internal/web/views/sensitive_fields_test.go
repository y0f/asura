package views

import (
	"reflect"
	"testing"
)

func TestSettingsSecretPathsKeepSeparateEditorsSeparate(t *testing.T) {
	p := MonitorFormParams{SecretsStored: map[string]bool{
		"target": true, "assertions": true, "body": true,
		"steps.0.url": true, "steps.0.headers.X.url": true, "password": false,
	}}
	want := []string{"body", "steps.0.headers.X.url", "steps.0.url"}
	if got := p.settingsSecretPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings paths = %v; want %v", got, want)
	}
}

func TestSecretURLLabelsDoNotMisclassifyHeaderNames(t *testing.T) {
	for key, want := range map[string]bool{
		"target": true, "oauth2_token_url": true, "steps.12.url": true,
		"body": false, "headers.url": false, "steps.0.headers.X.url": false,
		"steps.0.body": false, "args": false, "assertions": false,
	} {
		if got := secretPathIsURL(key); got != want {
			t.Errorf("%q replacement label = %v; want %v", key, got, want)
		}
	}
}
