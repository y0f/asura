package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

type settingsFailureStore struct {
	storage.Store
	sizeError bool
}

func (s settingsFailureStore) DBSize() (int64, error) {
	if s.sizeError {
		return 0, errors.New("injected size failure")
	}
	return 1024, nil
}

func (s settingsFailureStore) ListMonitors(context.Context, storage.MonitorListFilter, storage.Pagination) (*storage.PaginatedResult, error) {
	return nil, errors.New("injected monitor count failure")
}

func TestSettingsDoesNotPresentFailedReadsAsZero(t *testing.T) {
	for _, sizeError := range []bool{false, true} {
		h := testWebHandler(t)
		h.store = settingsFailureStore{sizeError: sizeError}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/settings", nil)
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{SuperAdmin: true}))
		h.Settings(w, r)
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "injected") {
			t.Fatalf("failed settings read presented as healthy or leaked internals: %d %s", w.Code, w.Body.String())
		}
	}
}

func configurationUpload(t *testing.T, body []byte, mode string) *http.Request {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("mode", mode); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("file", "configuration.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/asura/settings/import", &buffer)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{Name: "super", SuperAdmin: true}))
}

func TestConfigurationWebRendersFreshHeartbeatURLWithoutRedirect(t *testing.T) {
	h, store := storeHandler(t)
	h.cfg.Server.BasePath = "/asura"
	h.cfg.Server.ExternalURL = "https://monitor.example.test/asura"
	grace := 30
	file := api.ConfigurationFile{Version: 2, Monitors: []api.ConfigurationMonitor{{ID: 9, Name: "Nightly <backup>", Type: "heartbeat", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1, HeartbeatGrace: &grace}}}
	body, _ := json.Marshal(file)
	r := configurationUpload(t, body, "merge")
	w := httptest.NewRecorder()
	h.ImportConfig(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("one-time result used a redirect/cookie or could be cached: %d %v", w.Code, w.Header())
	}
	pattern := regexp.MustCompile(`https://monitor\.example\.test/asura/api/v1/heartbeat/([a-f0-9]{64})`)
	matches := pattern.FindAllStringSubmatch(w.Body.String(), -1)
	if len(matches) != 1 || !strings.Contains(w.Body.String(), "Nightly &lt;backup&gt;") {
		t.Fatal("result did not render one escaped enrollment field")
	}
	token := matches[0][1]
	if hb, err := store.GetHeartbeatByToken(context.Background(), token); err != nil || hb.Grace != grace {
		t.Fatalf("rendered credential not usable: %v", err)
	}
	w = httptest.NewRecorder()
	h.ImportConfig(w, configurationUpload(t, body, "merge"))
	if w.Code != http.StatusOK || pattern.Match(w.Body.Bytes()) {
		t.Fatalf("merge invented or redisplayed a credential: %d", w.Code)
	}
}

func TestConfigurationWebUploadRemovesTemporarySecretFile(t *testing.T) {
	h, _ := storeHandler(t)
	file := api.ConfigurationFile{Version: 2}
	settings, _ := json.Marshal(map[string]string{"body": strings.Repeat("private request data ", 60)})
	for i := range 1000 {
		file.Monitors = append(file.Monitors, api.ConfigurationMonitor{ID: int64(i + 1), Name: fmt.Sprintf("monitor%d", i), Type: "http", Target: "https://example.test", Interval: 60, Timeout: 10, Enabled: true, FailureThreshold: 1, SuccessThreshold: 1, Settings: settings})
	}
	body, _ := json.Marshal(file)
	if len(body) <= 1<<20 {
		t.Fatal("fixture must spill to a private temporary file")
	}
	r := configurationUpload(t, body, "merge")
	w := httptest.NewRecorder()
	h.ImportConfig(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("large upload rejected: %d %s", w.Code, w.Body.String())
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) != 1 {
		t.Fatal("multipart fixture did not parse")
	}
	if opened, err := r.MultipartForm.File["file"][0].Open(); err == nil {
		opened.Close()
		t.Fatal("successful import retained temporary plaintext upload")
	}
}

func TestConfigurationWebRejectsRedactedAndInvalidImportsWithoutMutation(t *testing.T) {
	h, store := storeHandler(t)
	for _, body := range []string{`{"version":2,"redacted":true}`, `{"version":2}{"version":2}`, `{"version":2,"monitor_groups":[{"id":1,"name":"partial"}],"monitors":[{"id":2,"name":"invalid"}]}`} {
		w := httptest.NewRecorder()
		h.ImportConfig(w, configurationUpload(t, []byte(body), "replace"))
		if w.Code != http.StatusSeeOther && w.Code != http.StatusFound {
			t.Fatalf("failed form import did not return to settings: %d", w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("import result may be cached")
		}
		got, err := store.SnapshotConfiguration(context.Background())
		if err != nil || len(got.Monitors)+len(got.Groups) != 0 {
			t.Fatalf("failed web import partially wrote configuration: %v", err)
		}
	}
}
