package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestTagWriteOnlyResponsesAndMutationErrors(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "tag writer", Hash: config.HashAPIKey("tag writer"), Permissions: []string{"tags.create", "tags.update", "tags.delete"}})
	}, false, nil)
	writer := map[string]string{"X-API-Key": "tag writer"}
	w := env.do("POST", "/api/v1/tags", `{"name":"new tag","color":"#005bbf"}`, writer)
	var created map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != 201 || len(created) != 1 || created["id"] <= 0 {
		t.Fatalf("write-only create disclosed tag: %d %s", w.Code, w.Body)
	}
	tag, err := env.store.GetTag(context.Background(), created["id"])
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/tags/%d", tag.ID)
	body := `{"name":"renamed","color":"#005bbf","created_at":"2000-01-01T00:00:00Z"}`
	w = env.do("PUT", path, body, writer)
	if w.Code != 200 || w.Body.String() != fmt.Sprintf("{\"id\":%d}\n", tag.ID) {
		t.Fatalf("write-only update disclosed stored metadata: %d %s", w.Code, w.Body)
	}
	w = env.do("PUT", path, body, admin)
	var updated storage.Tag
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil || w.Code != 200 || !updated.CreatedAt.Equal(tag.CreatedAt) || updated.Name != "renamed" {
		t.Fatalf("read-authorized mutation returned wrong committed row: %d %s", w.Code, w.Body)
	}
	w = env.do("POST", "/api/v1/tags", `{"name":"renamed","color":"#005bbf"}`, writer)
	if w.Code != 409 {
		t.Fatalf("duplicate create: %d %s", w.Code, w.Body)
	}
	other := &storage.Tag{Name: "occupied", Color: "#005bbf"}
	if err := env.store.CreateTag(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	w = env.do("PUT", path, `{"name":"occupied","color":"#005bbf"}`, writer)
	if w.Code != 409 {
		t.Fatalf("duplicate update: %d %s", w.Code, w.Body)
	}
	if w := env.do("DELETE", path, "", writer); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if w := env.do(method, path, body, writer); w.Code != 404 {
			t.Fatalf("missing %s: %d %s", method, w.Code, w.Body)
		}
	}
}
