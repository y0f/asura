package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/y0f/asura/v2/internal/model"
)

const defaultDockerSocket = "/var/run/docker.sock"

type DockerChecker struct{}

func (c *DockerChecker) Type() string { return "docker" }

func (c *DockerChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	var settings model.DockerSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	socketPath := settings.SocketPath
	if socketPath == "" {
		socketPath = defaultDockerSocket
	}

	containerName := settings.ContainerName
	if containerName == "" {
		containerName = monitor.Target
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport, Timeout: timeout}

	endpoint := fmt.Sprintf("http://docker/containers/%s/json", url.PathEscape(containerName))
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return &Result{
			Status:  "down",
			Message: fmt.Sprintf("request build failed: %v", err),
		}, nil
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("docker socket connection failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("read response failed: %v", err),
		}, nil
	}

	if resp.StatusCode == 404 {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			StatusCode:   resp.StatusCode,
			Message:      fmt.Sprintf("container %q not found", containerName),
		}, nil
	}

	if resp.StatusCode != 200 {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			StatusCode:   resp.StatusCode,
			Message:      fmt.Sprintf("docker api returned status %d", resp.StatusCode),
		}, nil
	}

	var inspect dockerInspect
	if err := json.Unmarshal(body, &inspect); err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("parse response failed: %v", err),
		}, nil
	}

	return evaluateContainerState(inspect, settings, elapsed), nil
}

type dockerInspect struct {
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func evaluateContainerState(inspect dockerInspect, settings model.DockerSettings, elapsed int64) *Result {
	if !inspect.State.Running {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("container status: %s", inspect.State.Status),
		}
	}

	if settings.CheckHealth && inspect.State.Health != nil {
		return containerHealthResult(inspect.State.Health.Status, elapsed)
	}

	return &Result{Status: "up", ResponseTime: elapsed, Message: "container running"}
}

func containerHealthResult(health string, elapsed int64) *Result {
	switch health {
	case "healthy":
		return &Result{Status: "up", ResponseTime: elapsed, Message: "container running, health: healthy"}
	case "starting":
		return &Result{Status: "degraded", ResponseTime: elapsed, Message: "container running, health: starting"}
	default:
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("container running, health: %s", health)}
	}
}
