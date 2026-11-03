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
	"sync"
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
	DNSRecords      string `json:"dns_records,omitempty"`
	// CheckedAt is when the check started. Results are posted once per cycle,
	// so the server uses it to ignore results older than its latest check.
	CheckedAt time.Time `json:"checked_at"`
}

func main() {
	serverURL := flag.String("server", "", "Asura server URL (e.g. https://monitor.example.com)")
	token := flag.String("token", "", "Agent token")
	interval := flag.Duration("interval", 30*time.Second, "Poll interval")
	concurrency := flag.Int("concurrency", 10, "Maximum number of checks run at the same time")
	allowPrivate := flag.Bool("allow-private", false, "Allow checks against private, loopback and other reserved addresses")
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

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a := newAgent(&http.Client{Timeout: 30 * time.Second}, strings.TrimRight(*serverURL, "/"), *token,
		checker.DefaultRegistry(nil, *allowPrivate), logger)
	a.concurrency = *concurrency

	logger.Info("starting asura-agent", "version", version, "server", a.server, "interval", interval.String(),
		"allow_private", *allowPrivate)

	// A signal cancels ctx, which aborts the in-flight cycle as well as the loop.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	a.runCycle(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return
		case <-ticker.C:
			a.runCycle(ctx)
		}
	}
}

// agent polls the server for jobs and runs each one when its monitor interval
// has elapsed.
type agent struct {
	client      *http.Client
	server      string
	token       string
	registry    *checker.Registry
	logger      *slog.Logger
	concurrency int

	lastRun map[int64]time.Time // monitor ID -> start of its last check
	now     func() time.Time
}

func newAgent(client *http.Client, server, token string, registry *checker.Registry, logger *slog.Logger) *agent {
	return &agent{
		client:      client,
		server:      server,
		token:       token,
		registry:    registry,
		logger:      logger,
		concurrency: 10,
		lastRun:     make(map[int64]time.Time),
		now:         time.Now,
	}
}

// dueSlack absorbs poll jitter, so a monitor whose interval equals the poll
// interval is not skipped every other cycle.
const dueSlack = 2 * time.Second

// due reports whether j should be checked now, given its interval.
func (a *agent) due(j job, now time.Time) bool {
	last, ok := a.lastRun[j.ID]
	return !ok || j.Interval <= 0 || now.Sub(last)+dueSlack >= time.Duration(j.Interval)*time.Second
}

func (a *agent) runCycle(ctx context.Context) {
	jobs, err := fetchJobs(ctx, a.client, a.server, a.token)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("fetch jobs failed", "error", err)
		}
		return
	}

	now := a.now()
	var due []job
	seen := make(map[int64]bool, len(jobs))
	for _, j := range jobs {
		seen[j.ID] = true
		if a.due(j, now) {
			due = append(due, j)
		}
	}
	for id := range a.lastRun {
		if !seen[id] {
			delete(a.lastRun, id)
		}
	}
	if len(due) == 0 {
		return
	}

	a.logger.Info("running checks", "count", len(due))

	results := make([]*result, len(due))
	sem := make(chan struct{}, max(a.concurrency, 1))
	var wg sync.WaitGroup
	for i, j := range due {
		c, err := a.registry.Get(j.Type)
		if err != nil {
			a.logger.Warn("unsupported monitor type", "type", j.Type, "monitor", j.Name)
			continue
		}
		a.lastRun[j.ID] = now

		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			results[i] = runCheck(ctx, c, j)
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		// Shutting down: checks were cut short, so their results are not real.
		return
	}

	var out []result
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	if len(out) > 0 {
		if err := postResults(ctx, a.client, a.server, a.token, out); err != nil {
			a.logger.Error("post results failed", "error", err)
		} else {
			a.logger.Info("results posted", "count", len(out))
		}
	}
}

func runCheck(ctx context.Context, c checker.Checker, j job) *result {
	mon := &storage.Monitor{
		ID:       j.ID,
		Name:     j.Name,
		Type:     j.Type,
		Target:   j.Target,
		Timeout:  j.Timeout,
		Settings: j.Settings,
	}

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(j.Timeout)*time.Second+5*time.Second)
	defer cancel()
	started := time.Now().UTC()
	res, err := c.Check(checkCtx, mon)
	if err != nil {
		return &result{MonitorID: j.ID, Status: "down", Message: err.Error(), CheckedAt: started}
	}
	out := &result{
		MonitorID:       j.ID,
		Status:          res.Status,
		ResponseTime:    res.ResponseTime,
		StatusCode:      res.StatusCode,
		Message:         res.Message,
		BodyHash:        res.BodyHash,
		CertFingerprint: res.CertFingerprint,
		CheckedAt:       started,
	}
	// The server evaluates dns_record assertions against these records.
	if len(res.DNSRecords) > 0 {
		b, _ := json.Marshal(res.DNSRecords)
		out.DNSRecords = string(b)
	}
	return out
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
