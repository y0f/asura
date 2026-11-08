package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestOnCallValidationAndActionBoundaries(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) {
		for name, permissions := range map[string][]string{
			"editor": {"on_call.update"}, "reader": {"on_call.read"},
			"override": {"on_call.override", "notifications.read"},
			"creator":  {"on_call.create", "notifications.read"},
		} {
			cfg.Auth.APIKeys = append(cfg.Auth.APIKeys, config.APIKeyConfig{Name: name, Hash: config.HashAPIKey(name), Permissions: permissions})
		}
	}, false, nil)
	ctx := context.Background()
	var channelIDs []int64
	for _, name := range []string{"private primary", "private backup"} {
		ch := &storage.NotificationChannel{Name: name, Type: "webhook", Settings: []byte("{}"), Enabled: true}
		if err := env.store.CreateNotificationChannel(ctx, ch); err != nil {
			t.Fatal(err)
		}
		channelIDs = append(channelIDs, ch.ID)
	}
	r := &storage.OnCallRotation{Name: "primary", Period: "daily", ChannelIDs: channelIDs}
	if err := env.store.CreateOnCallRotation(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/on-call/%d", r.ID)
	for _, body := range []string{
		`{"current_index":-1}`, `{"current_index":99}`, `{"period":"hourly"}`, `{"channel_ids":[]}`,
		`{"channel_ids":[99999],"current_index":0}`, `{"channel_ids":[1,1]}`, `{"name":" "}`, `null`, `{"unexpected":true}`,
		`{"override_channel_id":1,"override_until":"2000-01-01T00:00:00Z"}`,
	} {
		w := env.do(http.MethodPut, path, body, admin)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d: %s", body, w.Code, w.Body.String())
		}
	}
	got, err := env.store.GetOnCallRotation(ctx, r.ID)
	if err != nil || got.Name != r.Name || got.Period != r.Period || got.CurrentIndex != r.CurrentIndex {
		t.Fatalf("invalid request changed stored rotation: %+v %v", got, err)
	}
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	overrideBody := fmt.Sprintf(`{"override_channel_id":%d,"override_until":%q}`, channelIDs[1], expiry)
	for _, body := range []string{overrideBody, fmt.Sprintf(`{"channel_ids":[%d]}`, channelIDs[1])} {
		w := env.do(http.MethodPut, path, body, map[string]string{"X-API-Key": "editor"})
		if w.Code != http.StatusForbidden {
			t.Errorf("editor crossed relation/action boundary: %d %s", w.Code, w.Body.String())
		}
	}
	w := env.do(http.MethodPut, path, `{"name":"renamed"}`, map[string]string{"X-API-Key": "editor"})
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "renamed") || strings.Contains(w.Body.String(), "channel_ids") {
		t.Fatalf("write-only update failed or disclosed rotation: %d %s", w.Code, w.Body.String())
	}
	got, err = env.store.GetOnCallRotation(ctx, r.ID)
	if err != nil || got.Name != "renamed" || !got.PeriodStartedAt.Equal(r.PeriodStartedAt) || len(got.ChannelIDs) != 2 {
		t.Fatalf("partial name update lost cadence/relations: %+v %v", got, err)
	}
	w = env.do(http.MethodPost, path+"/override", overrideBody, map[string]string{"X-API-Key": "override"})
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "channel_ids") {
		t.Fatalf("override-only API action failed or disclosed rotation: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/v1/on-call/current", "", map[string]string{"X-API-Key": "reader"})
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private backup") || strings.Contains(w.Body.String(), "channel_name") {
		t.Fatalf("on-call read disclosed notification names: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Current []struct {
			ChannelID int64 `json:"channel_id"`
			Override  bool  `json:"override"`
		} `json:"on_call"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Current) != 1 || !result.Current[0].Override || result.Current[0].ChannelID != channelIDs[1] {
		t.Fatalf("active override not reflected: %+v %v", result, err)
	}
	w = env.do(http.MethodPost, path+"/override", `{"override_channel_id":null,"override_until":null}`, map[string]string{"X-API-Key": "override"})
	if w.Code != http.StatusOK {
		t.Fatalf("cannot clear override: %d %s", w.Code, w.Body.String())
	}
	got, err = env.store.GetOnCallRotation(ctx, r.ID)
	if err != nil || got.OverrideChannelID != nil || !got.PeriodStartedAt.Equal(r.PeriodStartedAt) {
		t.Fatalf("clear override changed cadence: %+v %v", got, err)
	}
	createBody := fmt.Sprintf(`{"name":"new","period":"weekly","channel_ids":[%d],"override_channel_id":%d,"override_until":%q}`, channelIDs[0], channelIDs[1], expiry)
	w = env.do(http.MethodPost, "/api/v1/on-call", createBody, map[string]string{"X-API-Key": "creator"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("creation bypassed override permission: %d %s", w.Code, w.Body.String())
	}
}
