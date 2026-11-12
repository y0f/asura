//go:build linux

package checker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func dockerTestSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(l)
	t.Cleanup(func() {
		srv.Close()
		os.Remove(sockPath)
	})
	return sockPath
}

func TestDockerCheckerRunning(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{SocketPath: sock})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "test-container",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "up" {
		t.Errorf("status = %q, want up (message: %s)", result.Status, result.Message)
	}
	if result.Message != "container running" {
		t.Errorf("message = %q, want %q", result.Message, "container running")
	}
}

func TestDockerCheckerStopped(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"exited","Running":false}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{SocketPath: sock})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "stopped-container",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Errorf("status = %q, want down", result.Status)
	}
}

func TestDockerCheckerNotFound(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"No such container"}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{SocketPath: sock})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "missing",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Errorf("status = %q, want down", result.Status)
	}
	if result.StatusCode != 404 {
		t.Errorf("status_code = %d, want 404", result.StatusCode)
	}
}

func TestDockerCheckerHealthy(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true,"Health":{"Status":"healthy"}}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath:  sock,
		CheckHealth: true,
	})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "healthy-app",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "up" {
		t.Errorf("status = %q, want up", result.Status)
	}
}

func TestDockerCheckerUnhealthy(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true,"Health":{"Status":"unhealthy"}}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath:  sock,
		CheckHealth: true,
	})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "unhealthy-app",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Errorf("status = %q, want down", result.Status)
	}
}

func TestDockerCheckerHealthStarting(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true,"Health":{"Status":"starting"}}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath:  sock,
		CheckHealth: true,
	})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "starting-app",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "degraded" {
		t.Errorf("status = %q, want degraded", result.Status)
	}
}

func TestDockerCheckerContainerNameFromSettings(t *testing.T) {
	var requestedPath string
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath:    sock,
		ContainerName: "from-settings",
	})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "from-target",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "up" {
		t.Errorf("status = %q, want up", result.Status)
	}
	if requestedPath != "/containers/from-settings/json" {
		t.Errorf("path = %q, want container name from settings", requestedPath)
	}
}

func TestDockerCheckerConnectionFailed(t *testing.T) {
	c := &DockerChecker{Sockets: []string{"/nonexistent/docker.sock"}}
	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath: "/nonexistent/docker.sock",
	})
	mon := &storage.Monitor{
		Target:   "test",
		Timeout:  1,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Errorf("status = %q, want down", result.Status)
	}
}

func TestDockerCheckerMalformedJSON(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`not json at all`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{SocketPath: sock})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "bad-response",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "down" {
		t.Errorf("status = %q, want down", result.Status)
	}
}

func TestDockerCheckerNoHealthIgnored(t *testing.T) {
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"State":{"Status":"running","Running":true}}`))
	}))

	settings, _ := json.Marshal(storage.DockerSettings{
		SocketPath:  sock,
		CheckHealth: true,
	})
	c := &DockerChecker{Sockets: []string{sock}}
	mon := &storage.Monitor{
		Target:   "no-healthcheck",
		Timeout:  5,
		Settings: settings,
	}

	result, err := c.Check(context.Background(), mon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "up" {
		t.Errorf("status = %q, want up (no healthcheck configured = running is fine)", result.Status)
	}
}

// A monitor must not reach a local socket the operator did not allow, even
// one that answers like the Docker API.
func TestDockerCheckerRejectsSocketOutsideAllowlist(t *testing.T) {
	requests := 0
	sock := dockerTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"State":{"Status":"running","Running":true}}`))
	}))
	settings, _ := json.Marshal(storage.DockerSettings{SocketPath: sock})
	mon := &storage.Monitor{Target: "test-container", Timeout: 5, Settings: settings}
	for name, c := range map[string]*DockerChecker{
		"default allowlist": {},
		"other socket":      {Sockets: []string{"/var/run/docker.sock"}},
	} {
		result, err := c.Check(context.Background(), mon)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "down" || result.Message != "docker socket not allowed: add it to monitor.docker_sockets" {
			t.Errorf("%s: got %q %q, want the socket rejected", name, result.Status, result.Message)
		}
	}
	if requests != 0 {
		t.Fatalf("a disallowed socket received %d requests", requests)
	}
}
