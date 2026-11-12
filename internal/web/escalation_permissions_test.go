package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

type unreadableEscalationChannels struct{ storage.Store }

func (unreadableEscalationChannels) ListNotificationChannels(context.Context) ([]*storage.NotificationChannel, error) {
	panic("policy reads must not fetch unrelated channel metadata")
}

type failedEscalationSave struct{ storage.Store }

func (failedEscalationSave) SaveEscalationPolicy(context.Context, int64, storage.EscalationPolicyPrepare) (*storage.EscalationPolicy, error) {
	return nil, errors.New("injected transaction failure")
}

func TestEscalationWebPreservesHiddenAssignmentsAndRejectsBrokenForms(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	ctx := context.Background()
	channel := &storage.NotificationChannel{Name: "hidden-channel", Type: "webhook", Settings: json.RawMessage(`{}`)}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	policy, err := store.SaveEscalationPolicy(ctx, 0, func(*storage.EscalationPolicy) (*storage.EscalationPolicy, error) {
		return &storage.EscalationPolicy{Name: "original", Enabled: true, Steps: []*storage.EscalationPolicyStep{{NotificationChannelIDs: []int64{channel.ID}}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.store = unreadableEscalationChannels{store}
	do := func(handler http.HandlerFunc, form url.Values, permissions ...string) *httptest.ResponseRecorder {
		method := http.MethodGet
		if form != nil {
			method = http.MethodPost
		}
		r := adminRequest(method, "/console/escalation-policies", form)
		r.SetPathValue("id", fmt.Sprint(policy.ID))
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, &config.APIKeyConfig{Name: "operator", Permissions: permissions}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := do(h.EscalationPolicies, nil, "escalation_policies.read", "escalation_policies.update")
	if w.Code != 200 || strings.Contains(w.Body.String(), channel.Name) || strings.Contains(w.Body.String(), `name="steps_present"`) || !strings.Contains(w.Body.String(), fmt.Sprintf("Channel #%d", channel.ID)) {
		t.Fatalf("policy page exposed channel editor/labels: %d %.300s", w.Code, w.Body.String())
	}
	w = do(h.EscalationPolicyUpdate, url.Values{"name": {"metadata-only"}, "enabled": {"on"}}, "escalation_policies.update", "notification_history.read")
	if w.Code != 303 || w.Header().Get("Location") != "/console/notifications/history" {
		t.Fatalf("metadata edit redirected to unreadable policy list: %d %v", w.Code, w.Header())
	}
	steps, err := store.GetEscalationPolicySteps(ctx, policy.ID)
	if err != nil || len(steps) != 1 || steps[0].ID != policy.Steps[0].ID || steps[0].NotificationChannelIDs[0] != channel.ID {
		t.Fatalf("hidden step assignment lost: %+v %v", steps, err)
	}
	for _, form := range []url.Values{
		{"name": {"not saved"}, "steps_present": {"1"}},
		{"name": {"not saved"}, "step_delay_minutes[]": {"bad"}, "step_channels[]": {"[1]"}},
		{"name": {"not saved"}, "step_delay_minutes[]": {"1"}, "step_channels[]": {"invalid"}},
		{"name": {"not saved"}, "step_delay_minutes[]": {"1", "2"}, "step_channels[]": {"[1]"}},
	} {
		w = do(h.EscalationPolicyUpdate, form, "escalation_policies.update", "notifications.read")
		if w.Code != 400 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("invalid form claimed success: %d %v", w.Code, w.Header())
		}
	}
	h.store = failedEscalationSave{store}
	w = do(h.EscalationPolicyUpdate, url.Values{"name": {"failed"}}, "escalation_policies.update")
	if w.Code != 500 || w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("failed save claimed success: %d %v", w.Code, w.Header())
	}
}
