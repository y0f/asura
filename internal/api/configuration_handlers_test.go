package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
)

type transferSnapshotStore struct {
	storage.Store
	snapshot *storage.ConfigurationSnapshot
	err      error
}

func (s transferSnapshotStore) SnapshotConfiguration(context.Context) (*storage.ConfigurationSnapshot, error) {
	return s.snapshot, s.err
}

func TestConfigurationHTTPExportNeverStartsPartialDownload(t *testing.T) {
	for _, store := range []transferSnapshotStore{
		{err: errors.New("injected snapshot failure")},
		{snapshot: &storage.ConfigurationSnapshot{Monitors: []*storage.Monitor{{ID: 1, Name: "partial", Settings: json.RawMessage(`invalid`)}}}},
	} {
		h := &Handler{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		r := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &config.APIKeyConfig{SuperAdmin: true}))
		w := httptest.NewRecorder()
		h.Export(w, r)
		if w.Code != http.StatusInternalServerError || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), `"version"`) {
			t.Fatalf("export started a partial successful download: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestConfigurationByteLimitIsEnforcedBeforeParsing(t *testing.T) {
	// A reader producing only whitespace avoids constructing a giant JSON
	// object while still exercising the actual read boundary.
	reader := io.LimitReader(configurationWhitespace{}, MaxConfigurationBytes+1)
	if _, err := DecodeConfiguration(reader); err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("oversized upload accepted: %v", err)
	}
}

type configurationWhitespace struct{}

func (configurationWhitespace) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = ' '
	}
	return len(buffer), nil
}
