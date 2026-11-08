package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
)

func TestGroupMutationsReturnOnlyIDWithoutReadAndRejectMissing(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "writer", Hash: config.HashAPIKey("writer"), Permissions: []string{"groups.create", "groups.update", "groups.delete"}})
	}, false, nil)
	headers := map[string]string{"X-API-Key": "writer"}
	w := env.do("POST", "/api/v1/groups", `{"name":"group","sort_order":2}`, headers)
	var result map[string]any
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 1 || result["id"] == nil {
		t.Fatalf("write-only create response: %d %s", w.Code, w.Body)
	}
	id := int64(result["id"].(float64))
	path := fmt.Sprintf("/api/v1/groups/%d", id)
	w = env.do("PUT", path, `{"name":"renamed","sort_order":3}`, headers)
	result = nil
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 1 {
		t.Fatalf("write-only update response: %d %s", w.Code, w.Body)
	}
	g, err := env.store.GetMonitorGroup(context.Background(), id)
	if err != nil || g.Name != "renamed" || g.CreatedAt.IsZero() || g.UpdatedAt.IsZero() {
		t.Fatal("group mutation lost stored metadata")
	}
	if w := env.do("DELETE", path, "", headers); w.Code != 200 {
		t.Fatal("group delete failed")
	}
	if w := env.do("PUT", path, `{"name":"missing"}`, headers); w.Code != 404 {
		t.Fatalf("missing update succeeded: %d", w.Code)
	}
}
