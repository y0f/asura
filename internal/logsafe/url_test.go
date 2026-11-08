package logsafe

import "testing"

func TestCredentialPathsIncludeRejectedAndCanonicalizedForms(t *testing.T) {
	for _, value := range []string{
		"/api/v1/heartbeat/private-token",
		"/asura/api/v1/heartbeat/private-token/extra",
		"/asura//api/./v1/heartbeat/private-token",
		"/asura//api/./v1/heartbeat/private-token/../../private-token",
		"/asura/API/v1/Heartbeat/private-token?more=secret#fragment",
	} {
		want := "/asura/api/v1/heartbeat/[redacted]"
		if value == "/api/v1/heartbeat/private-token" {
			want = "/api/v1/heartbeat/[redacted]"
		}
		if got := Path(value); got != want {
			t.Errorf("Path(%q)=%q want%q", value, got, want)
		}
	}
	for _, value := range []string{"", "/", "/asura/monitors/42", "/status/confirm", "/status/unsubscribe"} {
		if got := Path(value); got != value {
			t.Errorf("ordinary route changed: %q -> %q", value, got)
		}
	}
}

func TestRefererStripsCredentialsAndPreservesOrdinaryNavigation(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://user:password@status.example/ops/confirm?token=secret#fragment", "https://status.example/ops/confirm"},
		{"https://status.example/ops/unsubscribe?token=secret", "https://status.example/ops/unsubscribe"},
		{"https://status.example/asura/api/v1/heartbeat/token", "https://status.example/asura/api/v1/heartbeat/%5Bredacted%5D"},
		{"https://status.example/asura/api/v1/heartbeat%2Ftoken", "https://status.example/asura/api/v1/heartbeat/%5Bredacted%5D"},
		{"https://status.example/monitors/42", "https://status.example/monitors/42"},
		{"https://status.example/?", "https://status.example/"},
		{"https://status.example/%zz?token=secret", ""},
		{"https:secret", ""}, {"javascript:secret", ""}, {"/relative?token=secret", ""}, {"", ""},
	} {
		if got := Referer(tc.input); got != tc.want {
			t.Errorf("Referer(%q)=%q want%q", tc.input, got, tc.want)
		}
	}
}
