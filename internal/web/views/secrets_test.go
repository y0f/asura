package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestRedactSettings(t *testing.T) {
	raw := json.RawMessage(`{"method":"GET","bearer_token":"s3cret","basic_auth_pass":""}`)
	out, set := RedactSettings(raw, MonitorSecretKeys["http"])
	if strings.Contains(string(out), "s3cret") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !set["bearer_token"] || set["basic_auth_pass"] {
		t.Fatalf("unexpected set: %v", set)
	}
	if !strings.Contains(string(out), `"method":"GET"`) {
		t.Fatalf("non-secret field lost: %s", out)
	}
}

func TestMergeSecretsKeepsStoredWhenBlank(t *testing.T) {
	old := json.RawMessage(`{"bearer_token":"s3cret","method":"GET"}`)
	upd := json.RawMessage(`{"bearer_token":"","method":"POST"}`)
	got := MergeSecrets(upd, old, MonitorSecretKeys["http"], nil)
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["bearer_token"] != "s3cret" || m["method"] != "POST" {
		t.Fatalf("got %v", m)
	}
}

func TestMergeSecretsReplacesWhenProvided(t *testing.T) {
	old := json.RawMessage(`{"password":"old"}`)
	upd := json.RawMessage(`{"password":"new"}`)
	got := MergeSecrets(upd, old, MonitorSecretKeys["redis"], nil)
	if string(got) != string(upd) {
		t.Fatalf("got %s", got)
	}
}

func TestMergeSecretsClearRemovesStored(t *testing.T) {
	old := json.RawMessage(`{"password":"old"}`)
	upd := json.RawMessage(`{"password":""}`)
	got := MergeSecrets(upd, old, MonitorSecretKeys["redis"], map[string]bool{"password": true})
	if strings.Contains(string(got), "old") {
		t.Fatalf("cleared secret was restored: %s", got)
	}
}

func TestMergeSecretsHandlesNonObjectInput(t *testing.T) {
	old := json.RawMessage(`{"bearer_token":"s3cret"}`)
	for _, upd := range []string{"null", "[]", "42", `"x"`} {
		got := MergeSecrets(json.RawMessage(upd), old, MonitorSecretKeys["http"], nil)
		if string(got) != upd {
			t.Errorf("MergeSecrets(%s) = %s, want input unchanged", upd, got)
		}
	}
}

func TestMissingRequiredSecret(t *testing.T) {
	ch := &storage.NotificationChannel{Type: "discord", Settings: json.RawMessage(`{"webhook_url":""}`)}
	if MissingRequiredSecret(ch) != "webhook_url" {
		t.Fatal("empty webhook_url not reported")
	}
	ch.Settings = json.RawMessage(`{"webhook_url":"https://x"}`)
	if MissingRequiredSecret(ch) != "" {
		t.Fatal("present webhook_url reported missing")
	}
	ch = &storage.NotificationChannel{Type: "email", Settings: json.RawMessage(`{"host":"h"}`)}
	if MissingRequiredSecret(ch) != "" {
		t.Fatal("optional email password treated as required")
	}
}

func TestChannelEditJSONOmitsSecrets(t *testing.T) {
	ch := &storage.NotificationChannel{ID: 1, Name: "Ops", Type: "telegram", Settings: json.RawMessage(`{"bot_token":"123:abc","chat_id":"-100"}`)}
	out := ChannelEditJSON(ch)
	if strings.Contains(out, "123:abc") {
		t.Fatalf("token leaked: %s", out)
	}
	if !strings.Contains(out, `"secrets_set":["bot_token"]`) || !strings.Contains(out, `"chat_id":"-100"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestStatusToneAndLabel(t *testing.T) {
	cases := map[string]string{"up": "ok", "down": "crit", "degraded": "warn", "paused": "neutral", "open": "crit", "resolved": "ok"}
	for in, want := range cases {
		if got := StatusTone(in); got != want {
			t.Errorf("StatusTone(%q) = %q, want %q", in, got, want)
		}
	}
	if StatusLabel("") != "Pending" || StatusLabel("down") != "Down" {
		t.Error("unexpected labels")
	}
}

func TestUptimeToneThresholds(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{{100, "ok"}, {99.9, "ok"}, {99.5, "warn"}, {97, "major"}, {80, "crit"}}
	for _, c := range cases {
		if got := UptimeTone(c.pct); got != c.want {
			t.Errorf("UptimeTone(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestCapitalizeIsRuneSafe(t *testing.T) {
	if capitalize("über") != "Über" || capitalize("API") != "API" || capitalize("") != "" {
		t.Error("capitalize mishandled input")
	}
}

func TestPublicOverallWording(t *testing.T) {
	mk := func(statuses ...string) PublicStatusPageParams {
		p := PublicStatusPageParams{Overall: "major_outage"}
		for _, s := range statuses {
			p.Monitors = append(p.Monitors, MonitorWithUptime{Monitor: &storage.Monitor{Status: s}})
		}
		return p
	}
	if l, _ := publicOverall(mk("down", "up", "up", "up")); l != "Partial outage" {
		t.Errorf("one of four down: got %q", l)
	}
	if l, _ := publicOverall(mk("down", "down", "up")); l != "Major outage" {
		t.Errorf("two of three down: got %q", l)
	}
}
