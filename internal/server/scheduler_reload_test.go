package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/checker"
	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/incident"
	"github.com/y0f/asura/v3/internal/model"
	"github.com/y0f/asura/v3/internal/monitor"
	"github.com/y0f/asura/v3/internal/storage"
)

type reloadCommit struct {
	revision int64
	proxyURL string
}

type reloadRuntimeStore struct {
	*storage.SQLiteStore
	loads     chan int64
	committed chan reloadCommit
	failWrite atomic.Bool
}

func (s *reloadRuntimeStore) GetAllEnabledMonitors(ctx context.Context) ([]*storage.Monitor, error) {
	rows, err := s.SQLiteStore.GetAllEnabledMonitors(ctx)
	if err == nil && len(rows) == 1 {
		select {
		case s.loads <- rows[0].ConfigRevision:
		case <-ctx.Done():
		}
	}
	return rows, err
}

func (s *reloadRuntimeStore) RecordMonitorCheck(ctx context.Context, mon *storage.Monitor, result *storage.CheckResult, status *storage.MonitorStatus) error {
	if err := s.SQLiteStore.RecordMonitorCheck(ctx, mon, result, status); err != nil {
		return err
	}
	select {
	case s.committed <- reloadCommit{mon.ConfigRevision, mon.ProxyURL}:
	case <-ctx.Done():
	}
	return nil
}

func (s *reloadRuntimeStore) UpdateProxyWith(ctx context.Context, id int64, prepare storage.ProxyUpdate, access ...storage.MonitorAccess) (*storage.Proxy, error) {
	if s.failWrite.Load() {
		return nil, errors.New("injected proxy write failure")
	}
	return s.SQLiteStore.UpdateProxyWith(ctx, id, prepare, access...)
}

func (s *reloadRuntimeStore) DeleteAgent(ctx context.Context, id int64) ([]int64, error) {
	if s.failWrite.Load() {
		return nil, errors.New("injected agent delete failure")
	}
	return s.SQLiteStore.DeleteAgent(ctx, id)
}

type reloadRuntimeChecker struct{}

func (reloadRuntimeChecker) Type() string { return "http" }
func (reloadRuntimeChecker) Check(context.Context, *model.Monitor) (*checker.Result, error) {
	return &checker.Result{Status: "up", StatusCode: 200, ResponseTime: 1}, nil
}

func TestProxyAndAgentMutationsRefreshRunningScheduler(t *testing.T) {
	for _, endpoint := range []string{"api proxy", "web proxy", "api agent", "web agent"} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "runtime.db"), 2)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := &reloadRuntimeStore{SQLiteStore: db, loads: make(chan int64, 8), committed: make(chan reloadCommit, 8)}
			proxy := &storage.Proxy{Name: "route", Protocol: "http", Host: "old.proxy.test", Port: 8080, Enabled: true}
			if err := store.CreateProxy(ctx, proxy); err != nil {
				t.Fatal(err)
			}
			agent := &storage.Agent{Name: "region", Enabled: true}
			if err := store.CreateAgent(ctx, agent); err != nil {
				t.Fatal(err)
			}
			mon := &storage.Monitor{Name: "observed", Type: "http", Target: "https://example.test", Interval: 1, Timeout: 1,
				Enabled: true, FailureThreshold: 1, SuccessThreshold: 1}
			if strings.HasSuffix(endpoint, "proxy") {
				mon.ProxyID = &proxy.ID
			} else {
				mon.AgentEnabled = true
			}
			if err := store.CreateMonitor(ctx, mon); err != nil {
				t.Fatal(err)
			}
			if mon.AgentEnabled {
				if err := store.SetMonitorAgentIDs(ctx, mon.ID, []int64{agent.ID}); err != nil {
					t.Fatal(err)
				}
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			registry := checker.NewRegistry()
			registry.Register(reloadRuntimeChecker{})
			pipeline := monitor.NewPipeline(store, registry, incident.NewManager(store, logger), 1, false, logger)
			cfg := config.Defaults()
			cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "admin", SuperAdmin: true}}
			srv := NewServer(cfg, store, pipeline, nil, nil, logger, "test")
			defer srv.Close()
			done := make(chan struct{})
			go func() { defer close(done); pipeline.Run(ctx) }()
			defer func() { cancel(); <-done }()
			var initial int64
			select {
			case initial = <-store.loads:
			case <-time.After(3 * time.Second):
				t.Fatal("scheduler did not load initial snapshot")
			}
			select {
			case <-store.committed:
			case <-time.After(3 * time.Second):
				t.Fatal("initial check did not persist")
			}
			mutate := func() *httptest.ResponseRecorder {
				t.Helper()
				var body, contentType string
				id := agent.ID
				var handler http.HandlerFunc
				switch endpoint {
				case "api proxy":
					id, handler, body, contentType = proxy.ID, srv.api.UpdateProxy, `{"host":"new.proxy.test"}`, "application/json"
				case "web proxy":
					id, handler, contentType = proxy.ID, srv.web.ProxyUpdate, "application/x-www-form-urlencoded"
					body = url.Values{"name": {"route"}, "protocol": {"http"}, "host": {"new.proxy.test"}, "port": {"8080"}, "enabled": {"on"}}.Encode()
				case "api agent":
					handler = srv.api.DeleteAgentAPI
				case "web agent":
					handler = srv.web.AgentDelete
				}
				r := httptest.NewRequest(http.MethodPost, "/mutation", strings.NewReader(body))
				r.SetPathValue("id", fmt.Sprint(id))
				r.Header.Set("Content-Type", contentType)
				r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, &cfg.Auth.APIKeys[0]))
				w := httptest.NewRecorder()
				handler(w, r)
				return w
			}
			store.failWrite.Store(true)
			failed := mutate()
			if strings.HasPrefix(endpoint, "api") && failed.Code != http.StatusInternalServerError {
				t.Fatalf("injected failure status=%d body=%s", failed.Code, failed.Body)
			}
			select {
			case revision := <-store.loads:
				t.Fatalf("failed write reloaded scheduler revision%d", revision)
			case <-time.After(100 * time.Millisecond):
			}
			store.failWrite.Store(false)
			result := mutate()
			if result.Code != http.StatusOK && result.Code != http.StatusSeeOther {
				t.Fatalf("mutation failed: %d %s", result.Code, result.Body)
			}
			current, err := store.GetMonitor(ctx, mon.ID)
			if err != nil || current.ConfigRevision <= initial {
				t.Fatalf("precondition: referenced monitor revision did not change: %+v %v", current, err)
			}
			select {
			case revision := <-store.loads:
				if revision != current.ConfigRevision {
					t.Fatalf("scheduler reloaded stale revision%d want%d", revision, current.ConfigRevision)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("successful mutation did not refresh running scheduler")
			}
			deadline := time.After(3 * time.Second)
			for {
				select {
				case commit := <-store.committed:
					if commit.revision != current.ConfigRevision {
						continue
					}
					if mon.ProxyID != nil && commit.proxyURL != "http://new.proxy.test:8080" {
						t.Fatalf("reloaded checker used old proxy recipient: %q", commit.proxyURL)
					}
					return
				case <-deadline:
					t.Fatal("post-mutation check remained stale and never persisted")
				}
			}
		})
	}
}
