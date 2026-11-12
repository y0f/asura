package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/pageauth"
	"github.com/y0f/asura/v3/internal/storage"
)

// Command and Docker checks run on the server host. A key that can create and
// edit ordinary monitors must not create, edit, clone or convert to them.
func TestHostLocalMonitorsRequireSuperAdmin(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Auth.APIKeys = append(c.Auth.APIKeys, config.APIKeyConfig{Name: "editor", Hash: config.HashAPIKey("editor"),
			Permissions: []string{"monitors.read", "monitors.create", "monitors.update", "monitors.clone"}})
	}, false, nil)
	editor := map[string]string{"X-API-Key": "editor"}
	monitors := map[string]string{
		"command": `{"name":"cmd","type":"command","target":"command","interval":60,"timeout":10,"settings":{"command":"/usr/bin/true","args":["--x"]}}`,
		"docker":  `{"name":"ctr","type":"docker","target":"web","interval":60,"timeout":10,"settings":{"socket_path":"/var/run/docker.sock"}}`,
	}
	for kind, body := range monitors {
		t.Run(kind, func(t *testing.T) {
			if w := env.do("POST", "/api/v1/monitors", body, editor); w.Code != http.StatusForbidden {
				t.Fatalf("editor created a %s monitor: %d %s", kind, w.Code, w.Body)
			}
			w := env.do("POST", "/api/v1/monitors", body, admin)
			if w.Code != http.StatusCreated {
				t.Fatalf("super admin could not create a %s monitor: %d %s", kind, w.Code, w.Body)
			}
			var created storage.Monitor
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/monitors/" + strconv.FormatInt(created.ID, 10)
			if w := env.do("GET", path, "", editor); w.Code != http.StatusOK {
				t.Fatalf("editor could not read the %s monitor: %d", kind, w.Code)
			}
			if w := env.do("PUT", path, `{"name":"renamed","type":"`+kind+`","target":"x","interval":60,"timeout":10}`, editor); w.Code != http.StatusForbidden {
				t.Fatalf("editor edited a %s monitor: %d %s", kind, w.Code, w.Body)
			}
			if w := env.do("PUT", path, `{"name":"renamed","type":"http","target":"https://example.com","interval":60,"timeout":10}`, editor); w.Code != http.StatusForbidden {
				t.Fatalf("editor converted a %s monitor: %d %s", kind, w.Code, w.Body)
			}
			if w := env.do("POST", path+"/clone", "", editor); w.Code != http.StatusForbidden {
				t.Fatalf("editor cloned a %s monitor: %d %s", kind, w.Code, w.Body)
			}
			stored, err := env.store.GetMonitor(context.Background(), created.ID)
			if err != nil || stored.Type != kind || stored.Name == "renamed" {
				t.Fatalf("forbidden edit changed the monitor: %+v %v", stored, err)
			}
		})
	}

	w := env.do("POST", "/api/v1/monitors", `{"name":"web","type":"http","target":"https://example.com","interval":60,"timeout":10}`, editor)
	if w.Code != http.StatusCreated {
		t.Fatalf("editor could not create an http monitor: %d %s", w.Code, w.Body)
	}
	var web storage.Monitor
	json.Unmarshal(w.Body.Bytes(), &web)
	path := "/api/v1/monitors/" + strconv.FormatInt(web.ID, 10)
	if w := env.do("PUT", path, monitors["command"], editor); w.Code != http.StatusForbidden {
		t.Fatalf("editor turned an http monitor into a command monitor: %d %s", w.Code, w.Body)
	}
}

func subscribeRequest(env *testEnv, pageID int64, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/status-pages/"+strconv.FormatInt(pageID, 10)+"/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	env.srv.ServeHTTP(w, req)
	return w
}

// Public subscription is unauthenticated: it is rate limited per client,
// deduplicated without revealing existing subscribers, and capped per page.
func TestPublicSubscribeAbuseLimits(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		c.Subscriptions.Enabled = true
		c.Auth.Login.RateLimitPerSec = 0.001
		c.Auth.Login.RateLimitBurst = 2
	}, false, nil)
	sp := env.createStatusPage(t, `{"slug":"abuse","title":"Abuse","enabled":true,"api_enabled":true}`)
	ctx := context.Background()

	hook := `{"type":"webhook","webhook_url":"https://example.com/hook"}`
	if w := subscribeRequest(env, sp.ID, hook, "198.51.100.7"); w.Code != http.StatusCreated {
		t.Fatalf("first subscription: %d %s", w.Code, w.Body)
	}
	if w := subscribeRequest(env, sp.ID, hook, "198.51.100.7"); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "subscribed") {
		t.Fatalf("duplicate must look like a success: %d %s", w.Code, w.Body)
	}
	if n, _ := env.store.CountSubscribersByPage(ctx, sp.ID); n != 1 {
		t.Fatalf("duplicate stored a second row: %d subscribers", n)
	}
	if w := subscribeRequest(env, sp.ID, `{"type":"webhook","webhook_url":"https://example.com/other"}`, "198.51.100.7"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("third request from one client was not rate limited: %d %s", w.Code, w.Body)
	}
	// Rotating addresses inside one IPv6 /64 shares a single budget.
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2::3"} {
		w := subscribeRequest(env, sp.ID, fmt.Sprintf(`{"type":"webhook","webhook_url":"https://example.com/v6-%d"}`, i), ip)
		if want := http.StatusCreated; i == 2 {
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("IPv6 /64 rotation bypassed the limit: %d %s", w.Code, w.Body)
			}
		} else if w.Code != want {
			t.Fatalf("subscription %d from the /64: %d %s", i, w.Code, w.Body)
		}
	}
}

func TestPublicSubscribePageCap(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) { c.Subscriptions.Enabled = true }, false, nil)
	sp := env.createStatusPage(t, `{"slug":"full","title":"Full","enabled":true,"api_enabled":true}`)
	ctx := context.Background()
	for i := 0; i < storage.MaxSubscribersPerPage; i++ {
		if err := env.store.CreateStatusPageSubscriber(ctx, &storage.StatusPageSubscriber{
			StatusPageID: sp.ID, Type: "webhook", WebhookURL: fmt.Sprintf("https://example.com/%d", i), Confirmed: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	w := subscribeRequest(env, sp.ID, `{"type":"webhook","webhook_url":"https://example.com/one-more"}`, "203.0.113.9")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("subscription beyond the page cap: %d %s", w.Code, w.Body)
	}
	if n, _ := env.store.CountSubscribersByPage(ctx, sp.ID); n != storage.MaxSubscribersPerPage {
		t.Fatalf("page holds %d subscribers, want the cap %d", n, storage.MaxSubscribersPerPage)
	}
}

// A page still protected by a legacy unsalted SHA-256 hash is moved to a
// salted PBKDF2 hash the first time its password is verified.
func TestStatusPageHeaderPasswordUpgradesLegacyHash(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	sp := env.createStatusPage(t, `{"slug":"legacy","title":"Legacy","enabled":true,"api_enabled":true}`)
	ctx := context.Background()
	legacy := fmt.Sprintf("%x", sha256.Sum256([]byte("old-secret")))
	sp.PasswordHash = legacy
	if err := env.store.UpdateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(sp.ID, 10)
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", map[string]string{"X-Status-Page-Password": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if got, _ := env.store.GetStatusPage(ctx, sp.ID); got.PasswordHash != legacy {
		t.Fatal("a failed attempt changed the stored hash")
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", map[string]string{"X-Status-Page-Password": "old-secret"}); w.Code != http.StatusOK {
		t.Fatalf("correct legacy password: %d %s", w.Code, w.Body)
	}
	got, err := env.store.GetStatusPage(ctx, sp.ID)
	if err != nil || got.PasswordHash == legacy || pageauth.NeedsRehash(got.PasswordHash) || !pageauth.Verify(got.PasswordHash, "old-secret") {
		t.Fatalf("legacy hash was not upgraded: %q %v", got.PasswordHash, err)
	}
	if w := env.do("GET", "/api/v1/status-pages/"+id+"/public", "", map[string]string{"X-Status-Page-Password": "old-secret"}); w.Code != http.StatusOK {
		t.Fatalf("password stopped working after the upgrade: %d", w.Code)
	}
}
