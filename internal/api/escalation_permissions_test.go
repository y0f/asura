package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestEscalationRequestsKeepChannelAuthorityAndMetadataEditsSeparate(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "private-channel", Type: "webhook", Settings: json.RawMessage(`{}`)}
	if err := env.store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	h := api.New(config.Defaults(), env.store, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	do := func(handler http.HandlerFunc, id int64, body string, permissions ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/escalation-policies", strings.NewReader(body))
		r.SetPathValue("id", fmt.Sprint(id))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, &config.APIKeyConfig{Name: "operator", Permissions: permissions}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	body := fmt.Sprintf(`{"name":"original","enabled":true,"steps":[{"delay_minutes":0,"notification_channel_ids":[%d]}]}`, channel.ID)
	w := do(h.CreateEscalationPolicy, 0, body, "escalation_policies.create")
	if w.Code != 403 {
		t.Fatalf("create assigned unreadable channel: %d %s", w.Code, w.Body)
	}
	w = do(h.CreateEscalationPolicy, 0, body, "escalation_policies.create", "notifications.read")
	var created map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != 201 || len(created) != 1 || created["id"] == 0 {
		t.Fatalf("write-only create disclosed policy: %d %s %v", w.Code, w.Body, err)
	}
	id := created["id"]
	w = do(h.UpdateEscalationPolicy, id, `{"name":"metadata-only"}`, "escalation_policies.update")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != fmt.Sprintf(`{"id":%d}`, id) {
		t.Fatalf("metadata update/write-only response: %d %s", w.Code, w.Body)
	}
	stored, err := env.store.GetEscalationPolicy(ctx, id)
	if err != nil || stored.Name != "metadata-only" || !stored.Enabled {
		t.Fatalf("omitted policy fields lost: %+v %v", stored, err)
	}
	steps, err := env.store.GetEscalationPolicySteps(ctx, id)
	if err != nil || len(steps) != 1 || steps[0].NotificationChannelIDs[0] != channel.ID {
		t.Fatalf("hidden assignments cleared: %+v %v", steps, err)
	}
	for _, tc := range []struct {
		body string
		read bool
		want int
	}{
		{`{"name":"denied","steps":[]}`, false, 403},
		{`{"name":"denied","steps":[]}`, true, 400},
		{`{"name":"denied","steps":[null]}`, true, 400},
		{`{"name":"denied","steps":[{"notification_channel_ids":[999999]}]}`, true, 400},
		{fmt.Sprintf(`{"name":"denied","steps":[{"delay_minutes":153722868,"notification_channel_ids":[%d]}]}`, channel.ID), true, 400},
	} {
		permissions := []string{"escalation_policies.update"}
		if tc.read {
			permissions = append(permissions, "notifications.read")
		}
		w = do(h.UpdateEscalationPolicy, id, tc.body, permissions...)
		if w.Code != tc.want {
			t.Fatalf("invalid channel edit got%d want%d: %s", w.Code, tc.want, w.Body)
		}
		stored, err = env.store.GetEscalationPolicy(ctx, id)
		if err != nil || stored.Name != "metadata-only" {
			t.Fatalf("failed request partly edited policy: %+v %v", stored, err)
		}
	}
	w = do(h.GetEscalationPolicy, id, "", "escalation_policies.read")
	if w.Code != 200 || strings.Contains(w.Body.String(), channel.Name) || !strings.Contains(w.Body.String(), "notification_channel_ids") {
		t.Fatalf("related channel metadata escaped policy read: %d %s", w.Code, w.Body)
	}
	var editable storage.EscalationPolicy
	if err := json.Unmarshal(w.Body.Bytes(), &editable); err != nil {
		t.Fatal(err)
	}
	editable.Name = "round trip"
	roundTrip, err := json.Marshal(editable)
	if err != nil {
		t.Fatal(err)
	}
	w = do(h.UpdateEscalationPolicy, id, string(roundTrip), "escalation_policies.update")
	if w.Code != 200 {
		t.Fatalf("GET/edit/PUT failed on immutable response fields: %d %s", w.Code, w.Body)
	}

}
