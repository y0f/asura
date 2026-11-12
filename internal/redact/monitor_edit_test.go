package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestMonitorPrivateFieldsReadAndRetainedRoundTrip(t *testing.T) {
	for _, tc := range []struct{ kind, target, settings string }{
		{"http", "https://private-user:private-pass@example.test/private-path?token=private-query#private-fragment", `{"method":"POST","body":"private-body","bearer_token":"private-token","headers":{"X-Token":"private-header"},"oauth2_token_url":"https://auth.test/private-path?token=private-query","oauth2_client_secret":"private-client","expected_status":204}`},
		{"http_multi", "https://example.test/private-base", `{"steps":[{"name":"login","url":"https://example.test/private-login?key=private-query","body":"private-body","headers":{"Authorization":"private-header"}},{"name":"check","url":"https://example.test/private-check","body":"private-second"}]}`},
		{"command", "private-legacy-command-target", `{"command":"/usr/bin/check","args":["--token","private-argument"]}`},
		{"tcp", "example.test:443", `{"send_data":"private-payload","expect_data":"private-expected"}`},
		{"websocket", "wss://example.test/private-path", `{"send_message":"private-payload","expect_reply":"private-expected","headers":{"Authorization":"private-header"}}`},
		{"mqtt", "example.test:1883", `{"password":"private-password","expect_message":"private-expected","topic":"health"}`},
		{"smtp", "example.test:25", `{"expect_banner":"private-banner","port":25}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			old := &storage.Monitor{ID: 1, Type: tc.kind, Target: tc.target, Settings: json.RawMessage(tc.settings)}
			before, _ := json.Marshal(old)
			next := Monitor(old)
			read, _ := json.Marshal(next)
			if strings.Contains(string(read), "private-") {
				t.Fatalf("ordinary read exposed private data: %s", read)
			}
			if tc.kind == "http" && !strings.Contains(string(read), `"expected_status":204`) {
				t.Fatal("redaction hid ordinary numeric check metadata")
			}
			if err := MergeMonitorEdit(next, old, MonitorSecretKeys[tc.kind], nil, false); err != nil {
				t.Fatal(err)
			}
			if next.Target != old.Target || !sameSettings(next.Settings, old.Settings) {
				t.Fatalf("masked roundtrip lost stored fields: target=%v settings=%v", next.Target == old.Target, sameSettings(next.Settings, old.Settings))
			}
			after, _ := json.Marshal(old)
			if string(before) != string(after) {
				t.Fatal("read/merge mutated original storage object")
			}
		})
	}
}

func TestMonitorPrivateMalformedContainersFailClosed(t *testing.T) {
	for _, raw := range []string{
		`{"headers":"private-value"}`,
		`{"headers":["private-value"]}`,
		`{"headers":42}`,
		`{"steps":"private-value"}`,
		`{"steps":{"private-key":"private-value"}}`,
		`{"steps":["private-value"]}`,
		`{"steps":[null]}`,
		`{"steps":[{"url":"https://example.test/private-path","headers":"private-value"}]}`,
		`{"steps":[{"headers":["private-value"]}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			old := &storage.Monitor{Type: "http_multi", Settings: json.RawMessage(raw)}
			before, _ := json.Marshal(old)
			clean, _ := MonitorSettings(old.Settings)
			if string(clean) != `{}` {
				t.Fatalf("malformed private settings were not hidden: %s", clean)
			}
			next := &storage.Monitor{Type: old.Type, Settings: json.RawMessage(`{}`)}
			input, _ := json.Marshal(next)
			if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
				t.Fatal("malformed saved container was accepted")
			}
			after, _ := json.Marshal(old)
			merged, _ := json.Marshal(next)
			if string(before) != string(after) || string(input) != string(merged) {
				t.Fatal("failed read/merge mutated stored data or input")
			}
		})
	}
}

func TestMonitorPrivateStepMatchingBoundedBeforeMerge(t *testing.T) {
	for _, count := range []int{6, 1000} {
		steps := make([]map[string]string, count)
		for i := range steps {
			steps[i] = map[string]string{"name": "same", "url": "https://example.test/private-path", "body": "private-body"}
		}
		raw, err := json.Marshal(map[string]any{"steps": steps})
		if err != nil {
			t.Fatal(err)
		}
		for _, oversizedOld := range []bool{false, true} {
			old := &storage.Monitor{Type: "http_multi", Settings: json.RawMessage(`{}`)}
			next := &storage.Monitor{Type: old.Type, Settings: raw}
			if oversizedOld {
				old.Settings, next.Settings = raw, old.Settings
			}
			before, _ := json.Marshal(next)
			if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil || !strings.Contains(err.Error(), "at most 5") {
				t.Fatalf("%d steps did not fail the early bound: %v", count, err)
			}
			after, _ := json.Marshal(next)
			if string(before) != string(after) {
				t.Fatal("oversized merge changed input")
			}
		}
		clean, _ := MonitorSettings(raw)
		if string(clean) != `{}` {
			t.Fatal("oversized legacy step container was exposed")
		}
	}
}

func TestMonitorPrivatePayloadDestinationAndExplicitClearing(t *testing.T) {
	for _, key := range monitorPayloadKeys {
		t.Run(key, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{key: "private-saved"})
			old := &storage.Monitor{Type: "http", Target: "https://old.test", Settings: raw}
			for _, settings := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"` + key + `":""}`)} {
				next := &storage.Monitor{Type: old.Type, Target: "https://new.test", Settings: settings}
				before, _ := json.Marshal(next)
				if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil || strings.Contains(err.Error(), "private-saved") {
					t.Fatal("retarget reused saved payload or disclosed it in error")
				}
				after, _ := json.Marshal(next)
				if string(before) != string(after) {
					t.Fatal("failed merge mutated caller input")
				}
				if err := MergeMonitorEdit(next, old, nil, map[string]bool{key: true}, false); err != nil || strings.Contains(string(next.Settings), "private-saved") {
					t.Fatalf("explicit clear failed: %v", err)
				}
			}
		})
	}
	old := &storage.Monitor{Type: "command", Target: "command", Settings: json.RawMessage(`{"command":"/usr/bin/old","args":["private-argument"]}`)}
	next := &storage.Monitor{Type: "command", Target: "", Settings: json.RawMessage(`{"command":"/usr/bin/new","args":[]}`)}
	if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
		t.Fatal("new executable received hidden stored arguments")
	}
	if err := MergeMonitorEdit(next, old, nil, map[string]bool{"args": true}, false); err != nil || strings.Contains(string(next.Settings), "private-argument") {
		t.Fatalf("argument clearing failed: %v", err)
	}
}

func TestMonitorPrivateURLsRequireExplicitReplacementForNewProxy(t *testing.T) {
	proxy := int64(2)
	for _, omitted := range []bool{false, true} {
		old := &storage.Monitor{Type: "http", Target: "https://example.test/private-path?secret=private-query", Settings: json.RawMessage(`{}`)}
		next := Monitor(old)
		next.ProxyID = &proxy
		if omitted {
			next.Target, next.Settings = "", nil
		}
		if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
			t.Fatal("new proxy inherited hidden target credentials")
		}
		next.Target = "https://example.test"
		if err := MergeMonitorEdit(next, old, nil, map[string]bool{"target": true}, false); err != nil || next.Target != "https://example.test" {
			t.Fatalf("explicit origin replacement failed: %v", err)
		}
	}
	old := &storage.Monitor{Type: "http", Target: "https://example.test", Settings: json.RawMessage(`{"oauth2_token_url":"https://auth.test/private-token"}`)}
	next := Monitor(old)
	next.ProxyID = &proxy
	if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil {
		t.Fatal("new proxy inherited hidden OAuth URL credentials")
	}
	if err := MergeMonitorEdit(next, old, nil, map[string]bool{"oauth2_token_url": true}, false); err != nil || strings.Contains(string(next.Settings), "private-token") {
		t.Fatalf("explicit token URL replacement failed: %v", err)
	}
}

func TestMonitorPrivateStepsReorderAndAmbiguity(t *testing.T) {
	old := &storage.Monitor{Type: "http_multi", Target: "https://example.test", Settings: json.RawMessage(`{"steps":[{"name":"one","url":"https://example.test/private-one","body":"private-body-one","headers":{"X-Key":"private-key-one"}},{"name":"two","url":"https://example.test/private-two","body":"private-body-two","headers":{"X-Key":"private-key-two"}}]}`)}
	next := &storage.Monitor{Type: old.Type, Target: old.Target, Settings: json.RawMessage(`{"steps":[{"name":"two","url":"https://example.test","body":"","headers":{"x-key":""}},{"name":"one","url":"https://example.test","body":"","headers":{"X-Key":""}}]}`)}
	if err := MergeMonitorEdit(next, old, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	var settings storage.MultiStepHTTPSettings
	if err := json.Unmarshal(next.Settings, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Steps[0].URL != "https://example.test/private-two" || settings.Steps[0].Body != "private-body-two" || settings.Steps[0].Headers["x-key"] != "private-key-two" {
		t.Fatal("reorder attached another step's URL, body or header")
	}
	old.Settings = json.RawMessage(`{"steps":[{"name":"same","url":"https://example.test/private-one"},{"name":"same","url":"https://example.test/private-two"}]}`)
	next = Monitor(old)
	if err := MergeMonitorEdit(next, old, nil, nil, false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("colliding redacted step identities silently reused credentials")
	}
	// Omitting the entire settings object cannot reorder steps and safely keeps
	// the current transaction's configuration, including legacy duplicate names.
	next.Settings = nil
	if err := MergeMonitorEdit(next, old, nil, nil, false); err != nil || !sameSettings(next.Settings, old.Settings) {
		t.Fatalf("omitted duplicate steps were not preserved: %v", err)
	}
}

func TestMonitorPrivateMergeClearHeadersAndAdministratorRetarget(t *testing.T) {
	old := &storage.Monitor{Type: "http", Target: "https://old.test", Settings: json.RawMessage(`{"headers":{"X-Key":"private-header"},"bearer_token":"private-token","body":"private-body"}`)}
	next := Monitor(old)
	next.Target = "https://new.test"
	if err := MergeMonitorEdit(next, old, MonitorSecretKeys["http"], nil, false); err == nil {
		t.Fatal("retarget preserved saved credentials")
	}
	clear := map[string]bool{"headers.x-KEY": true, "bearer_token": true, "body": true}
	if err := MergeMonitorEdit(next, old, MonitorSecretKeys["http"], clear, false); err != nil || strings.Contains(string(next.Settings), "private-") {
		t.Fatalf("case-insensitive explicit clears failed: %v", err)
	}
	next = Monitor(old)
	next.Target = "https://new.test"
	if err := MergeMonitorEdit(next, old, MonitorSecretKeys["http"], nil, true); err != nil || !strings.Contains(string(next.Settings), "private-token") {
		t.Fatalf("explicit administrator authority did not permit retarget: %v", err)
	}
}

func sameSettings(a, b json.RawMessage) bool {
	var first, second any
	return json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil && reflect.DeepEqual(first, second)
}

func TestMonitorPrivateURLProjectionAndReplacement(t *testing.T) {
	for _, tc := range []struct{ kind, target, want string }{
		{"http", "https://user:private@example.test/private-path?secret=private#private", "https://example.test"},
		{"http", "https://private%xx@example.test/path", ""},
		{"http", "https://example.test/%xx-private", ""},
		{"http", "private-invalid", ""},
		{"http_multi", "{{private_dynamic_url}}", ""},
		{"postgresql", "host=example.test password=private", ""},
		{"postgresql", "user:private@example.test:5432", ""},
		{"unknown", "private-target", ""},
		{"tcp", "example.test:443", "example.test:443"},
		{"tcp", "[2001:db8::1]:443", "[2001:db8::1]:443"},
		{"icmp", "2001:db8::1", "2001:db8::1"},
		{"docker", "container-name_1", "container-name_1"},
	} {
		if got := MonitorTarget(tc.kind, tc.target); got != tc.want {
			t.Fatalf("unsafe or incorrect %s target projection: got%q want%q", tc.kind, got, tc.want)
		}
	}
	old := &storage.Monitor{Type: "http", Target: "https://example.test/private-path?key=private-query", Settings: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		target string
		clear  bool
		want   string
	}{
		{"https://example.test", false, old.Target},
		{"", false, old.Target},
		{"https://example.test", true, "https://example.test"},
		{"https://example.test/replacement?key=new", false, "https://example.test/replacement?key=new"},
	} {
		next := &storage.Monitor{Type: old.Type, Target: tc.target}
		if err := MergeMonitorEdit(next, old, nil, map[string]bool{"target": tc.clear}, false); err != nil || next.Target != tc.want {
			t.Fatalf("origin roundtrip/replacement: got%q want%q err=%v", next.Target, tc.want, err)
		}
	}
}

func TestMonitorPrivateMergeRejectsMalformedStepIdentityAndHeaders(t *testing.T) {
	for _, raw := range []string{
		`{"steps":[{"name":{},"url":"https://example.test/private"}]}`,
		`{"steps":[{"name":[],"url":"https://example.test/private"}]}`,
		`{"headers":{"X-Key":"private-first","x-key":"private-second"}}`,
		`{"steps":[{"name":"one","url":"https://example.test","headers":{"X-Key":"private-first","x-key":"private-second"}}]}`,
	} {
		old := &storage.Monitor{Type: "http_multi", Target: "https://example.test", Settings: json.RawMessage(raw)}
		next := *old
		if err := MergeMonitorEdit(&next, old, nil, nil, false); err == nil || strings.Contains(err.Error(), "private-") {
			t.Fatal("malformed identity/header ambiguity did not fail closed")
		}
	}
}
