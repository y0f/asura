package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

func TestConfigurationTransferRequiresSuperAdmin(t *testing.T) {
	const data = `{"version":1,"notification_channels":[{"name":"imported","type":"webhook","enabled":false,"settings":{"url":"https://127.0.0.1/test"}}]}`
	for _, tc := range []struct {
		name, method, path string
		web, replace       bool
	}{
		{"API export", "GET", "/api/v1/export", false, false},
		{"API redacted export", "GET", "/api/v1/export?redact_secrets=true", false, false},
		{"API merge", "POST", "/api/v1/import", false, false},
		{"API replace", "POST", "/api/v1/import?mode=replace", false, true},
		{"web export", "GET", "/settings/export?redact_secrets=false", true, false},
		{"web redacted export", "GET", "/settings/export?redact_secrets=true", true, false},
		{"web merge", "POST", "/settings/import", true, false},
		{"web replace", "POST", "/settings/import", true, true},
	} {
		for _, role := range []string{"monitor writer", "all permissions", "super admin"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				srv, _ := testServer(t)
				t.Cleanup(srv.Close)
				key := config.APIKeyConfig{Name: "transfer", Hash: config.HashAPIKey("transfer-key"), Permissions: []string{"monitors.write"}, SuperAdmin: role == "super admin"}
				if role == "all permissions" {
					key.Permissions = config.AllPermissions
				}
				srv.cfg.Auth.APIKeys = append(srv.cfg.Auth.APIKeys, key)
				channel := &storage.NotificationChannel{Name: "existing", Type: "webhook", Settings: json.RawMessage(`{"url":"https://127.0.0.1/existing"}`)}
				if err := srv.store.CreateNotificationChannel(context.Background(), channel); err != nil {
					t.Fatal(err)
				}
				body, contentType := io.Reader(strings.NewReader(data)), "application/json"
				if tc.web && tc.method == http.MethodPost {
					var buf bytes.Buffer
					writer := multipart.NewWriter(&buf)
					mode := "merge"
					if tc.replace {
						mode = "replace"
					}
					if err := writer.WriteField("mode", mode); err != nil {
						t.Fatal(err)
					}
					file, err := writer.CreateFormFile("file", "config.json")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(file, data); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					body, contentType = &buf, writer.FormDataContentType()
				}
				req := httptest.NewRequest(tc.method, tc.path, body)
				req.Header.Set("Content-Type", contentType)
				if tc.web {
					req.Header.Set("Origin", "http://example.com")
					if role == "monitor writer" {
						// Exercise a surviving session after its read permission is removed.
						srv.cfg.Auth.APIKeys[len(srv.cfg.Auth.APIKeys)-1].Permissions = []string{"monitors.read", "monitors.write"}
					}
					req.AddCookie(webLoginCookie(t, srv, "transfer-key"))
					srv.cfg.Auth.APIKeys[len(srv.cfg.Auth.APIKeys)-1].Permissions = key.Permissions
				} else {
					req.Header.Set("X-API-Key", "transfer-key")
				}
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				wantStatus := http.StatusForbidden
				if key.SuperAdmin {
					wantStatus = http.StatusOK
				}
				if w.Code != wantStatus {
					t.Errorf("status = %d, want %d: %s", w.Code, wantStatus, w.Body.String())
				}
				channels, err := srv.store.ListNotificationChannels(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				seen := map[string]bool{}
				for _, ch := range channels {
					seen[ch.Name] = true
				}
				imported := key.SuperAdmin && tc.method == http.MethodPost
				if seen["imported"] != imported || (!imported && !seen["existing"]) {
					t.Errorf("unexpected stored channels: %v", seen)
				}
			})
		}
	}
}
