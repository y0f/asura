package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/storage"
)

var version = "dev"

type job struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Target   string          `json:"target"`
	Interval int             `json:"interval"`
	Timeout  int             `json:"timeout"`
	Settings json.RawMessage `json:"settings"`
}

type result struct {
	MonitorID       int64  `json:"monitor_id"`
	Status          string `json:"status"`
	ResponseTime    int64  `json:"response_time"`
	StatusCode      int    `json:"status_code"`
	Message         string `json:"message"`
	BodyHash        string `json:"body_hash"`
	CertFingerprint string `json:"cert_fingerprint"`
	DNSRecords      string `json:"dns_records"`
}

func main() {
	serverURL := flag.String("server", "", "Asura server URL (e.g. https://monitor.example.com)")
	token := flag.String("token", "", "Agent token")
	interval := flag.Duration("interval", 30*time.Second, "Poll interval")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("asura-agent %s\n", version)
		os.Exit(0)
	}

	if *serverURL == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "Usage: asura-agent --server URL --token TOKEN")
		os.Exit(1)
	}

	server := strings.TrimRight(*serverURL, "/")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	registry := checker.DefaultRegistry(nil, false)

	logger.Info("starting asura-agent", "version", version, "server", server, "interval", interval.String())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	client := &http.Client{Timeout: 30 * time.Second}

	runCycle(ctx, client, server, *token, registry, logger)

	for {
		select {
		case <-quit:
			logger.Info("shutting down")
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle(ctx, client, server, *token, registry, logger)
		}
	}
}

func runCycle(ctx context.Context, client *http.Client, server, token string, registry *checker.Registry, logger *slog.Logger) {
	jobs, err := fetchJobs(ctx, client, server, token)
	if err != nil {
		logger.Error("fetch jobs failed", "error", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	logger.Info("running checks", "count", len(jobs))

	var results []result
	for _, j := range jobs {
		c, err := registry.Get(j.Type)
		if err != nil {
			logger.Warn("unsupported monitor type", "type", j.Type, "monitor", j.Name)
			continue
		}

		mon := &storage.Monitor{
			ID:       j.ID,
			Name:     j.Name,
			Type:     j.Type,
			Target:   j.Target,
			Timeout:  j.Timeout,
			Settings: j.Settings,
		}

		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(j.Timeout)*time.Second+5*time.Second)
		res, err := c.Check(checkCtx, mon)
		cancel()

		if err != nil {
			results = append(results, result{
				MonitorID: j.ID,
				Status:    "down",
				Message:   err.Error(),
			})
			continue
		}

		results = append(results, result{
			MonitorID:       j.ID,
			Status:          res.Status,
			ResponseTime:    res.ResponseTime,
			StatusCode:      res.StatusCode,
			Message:         res.Message,
			BodyHash:        res.BodyHash,
			CertFingerprint: res.CertFingerprint,
		})
	}

	if len(results) > 0 {
		if err := postResults(ctx, client, server, token, results); err != nil {
			logger.Error("post results failed", "error", err)
		} else {
			logger.Info("results posted", "count", len(results))
		}
	}
}

func fetchJobs(ctx context.Context, client *http.Client, server, token string) ([]job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/agent/jobs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Agent-Token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Jobs []job `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode jobs: %w", err)
	}
	return response.Jobs, nil
}

func postResults(ctx context.Context, client *http.Client, server, token string, results []result) error {
	body, err := json.Marshal(results)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/agent/results", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Agent-Token", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}
