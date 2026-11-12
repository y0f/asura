package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/y0f/asura/v2/internal/checker"
	"github.com/y0f/asura/v2/internal/model"
)

var version = "dev"

type job struct {
	ConfigRevision int64           `json:"config_revision"`
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Target         string          `json:"target"`
	Interval       int             `json:"interval"`
	Timeout        int             `json:"timeout"`
	Settings       json.RawMessage `json:"settings"`
}

type result struct {
	ConfigRevision  int64  `json:"config_revision"`
	MonitorID       int64  `json:"monitor_id"`
	Status          string `json:"status"`
	ResponseTime    int64  `json:"response_time"`
	StatusCode      int    `json:"status_code"`
	Message         string `json:"message"`
	BodyHash        string `json:"body_hash"`
	CertFingerprint string `json:"cert_fingerprint"`
	DNSRecords      string `json:"dns_records,omitempty"`
	// CheckedAt is when the check started. Results are posted in bounded batches,
	// so the server uses it to ignore results older than its latest check.
	CheckedAt time.Time `json:"checked_at"`
}

func main() {
	serverURL := flag.String("server", "", "Asura server URL (e.g. https://monitor.example.com)")
	tokenFile := flag.String("token-file", "", "File containing the agent token (default: the "+tokenEnv+" environment variable)")
	tokenFlag := flag.String("token", "", "Not supported: command-line arguments are visible to other local users; use --token-file or "+tokenEnv)
	insecureHTTP := flag.Bool("insecure-http", false, "Allow a plain-HTTP server URL on a non-loopback host (sends the token unencrypted)")
	interval := flag.Duration("interval", 30*time.Second, "Poll interval")
	concurrency := flag.Int("concurrency", 10, "Maximum number of checks run at the same time")
	allowPrivate := flag.Bool("allow-private", false, "Allow checks against private, loopback and other reserved addresses")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("asura-agent %s\n", version)
		os.Exit(0)
	}

	if *tokenFlag != "" {
		fmt.Fprintln(os.Stderr, "--token is not supported because command-line arguments are visible to other local users; set "+tokenEnv+" or use --token-file")
		os.Exit(1)
	}
	token, err := resolveToken(*tokenFile, os.Getenv(tokenEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server, err := validateServerURL(*serverURL, *insecureHTTP)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *interval <= 0 || *concurrency <= 0 {
		fmt.Fprintln(os.Stderr, "interval and concurrency must be positive")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a := newAgent(&http.Client{Timeout: 30 * time.Second}, server, token,
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

const tokenEnv = "ASURA_AGENT_TOKEN"

// resolveToken reads the agent token from file when given, else from the
// environment value. Surrounding whitespace (such as a trailing newline) is
// ignored; an empty token is an error.
func resolveToken(file, env string) (string, error) {
	token := env
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read token file: %w", err)
		}
		token = string(data)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("agent token is required: set %s or use --token-file", tokenEnv)
	}
	if strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("agent token must be a single line")
	}
	return token, nil
}

// validateServerURL accepts an absolute HTTPS URL, or plain HTTP to a loopback
// host. Other plain-HTTP URLs would send the token unencrypted and need
// insecureHTTP. The URL may not carry credentials, a query or a fragment.
func validateServerURL(raw string, insecureHTTP bool) (string, error) {
	if raw == "" {
		return "", errors.New("--server is required, e.g. --server https://monitor.example.com")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("--server must be an absolute URL such as https://monitor.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("--server must not contain credentials, a query or a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !insecureHTTP && !isLoopbackHost(u.Hostname()) {
			return "", errors.New("--server uses plain HTTP, which would send the agent token unencrypted; use https:// or pass --insecure-http")
		}
	default:
		return "", errors.New("--server must use https://")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

	lastRun map[int64]checkRun // monitor ID -> last acknowledged check
	now     func() time.Time
}

type checkRun struct {
	started  time.Time
	revision int64
}

func newAgent(client *http.Client, server, token string, registry *checker.Registry, logger *slog.Logger) *agent {
	// The control-plane token belongs to the configured endpoint. Copy the
	// client so enforcing this boundary does not change a caller's transport.
	controlClient := *client
	controlClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("agent server redirects are not allowed")
	}
	return &agent{
		client:      &controlClient,
		server:      server,
		token:       token,
		registry:    registry,
		logger:      logger,
		concurrency: 10,
		lastRun:     make(map[int64]checkRun),
		now:         time.Now,
	}
}

// dueSlack absorbs poll jitter, so a monitor whose interval equals the poll
// interval is not skipped every other cycle.
const dueSlack = 2 * time.Second

const (
	maxJobsBytes        = 32 << 20
	maxResultBatchBytes = 8 << 20 // Below the server's 10 MiB request limit.
)

// due reports whether j should be checked now, given its interval.
func (a *agent) due(j job, now time.Time) bool {
	last, ok := a.lastRun[j.ID]
	return !ok || last.revision != j.ConfigRevision || j.Interval <= 0 || now.Sub(last.started)+dueSlack >= time.Duration(j.Interval)*time.Second
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
			// Only successfully acknowledged fresh observations restore a due
			// job's schedule. Failures and cancellation retry on the next poll.
			delete(a.lastRun, j.ID)
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

	cycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := min(max(a.concurrency, 1), len(due))
	results := make(chan *result, workers)
	// Bound active checks, waiting goroutines and completed results. Slow result
	// submissions apply backpressure instead of retaining the whole cycle.
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cycleCtx.Err() == nil {
				i := int(next.Add(1)) - 1
				if i >= len(due) {
					return
				}
				j := due[i]
				c, err := a.registry.Get(j.Type)
				if err != nil {
					a.logger.Warn("unsupported monitor type", "type", j.Type, "monitor", j.ID)
					continue
				}
				r := runCheck(cycleCtx, c, j, a.now().UTC())
				select {
				case results <- r:
				case <-cycleCtx.Done():
					return
				}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()
	batch := newResultBatches(cycleCtx, a.client, a.server, a.token, func(receipt resultReceipt) {
		a.lastRun[receipt.monitorID] = receipt.checkRun
	})
	var postErr error
	for r := range results {
		if cycleCtx.Err() == nil {
			if postErr = batch.add(*r, 0); postErr != nil {
				// Stop unfinished checks and join all senders before returning.
				cancel()
			}
		}
	}
	if ctx.Err() != nil {
		return // Never submit checks cut short by shutdown.
	}
	if err := batch.finish(postErr); err != nil {
		a.logger.Error("post results failed", "error", err)
	} else if batch.posted > 0 {
		a.logger.Info("results posted", "count", batch.posted)
	}
}

func runCheck(ctx context.Context, c checker.Checker, j job, started time.Time) *result {
	mon := &model.Monitor{
		ID:       j.ID,
		Name:     j.Name,
		Type:     j.Type,
		Target:   j.Target,
		Timeout:  j.Timeout,
		Settings: j.Settings,
	}

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(j.Timeout)*time.Second+5*time.Second)
	defer cancel()
	res, err := c.Check(checkCtx, mon)
	if err != nil {
		return &result{MonitorID: j.ID, ConfigRevision: j.ConfigRevision, Status: "down", Message: err.Error(), CheckedAt: started}
	}
	out := &result{
		ConfigRevision:  j.ConfigRevision,
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
		// A reverse proxy may echo credentials or private request data in its
		// error body. Never load that body into the agent's error/log output.
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	return decodeJobs(resp.Body, maxJobsBytes)
}

func decodeJobs(body io.Reader, limit int64) ([]job, error) {
	bounded := &io.LimitedReader{R: body, N: limit + 1}
	decoder := json.NewDecoder(bounded)
	var response *struct {
		Jobs []job `json:"jobs"`
	}
	err := decoder.Decode(&response)
	if bounded.N == 0 {
		return nil, fmt.Errorf("jobs response exceeds %d bytes", limit)
	}
	if err != nil {
		return nil, fmt.Errorf("decode jobs: %w", err)
	}
	if response == nil {
		return nil, errors.New("jobs response must contain one JSON object")
	}
	// Consume through EOF so a second JSON value or excessive trailing data
	// cannot bypass the size limit or hide a truncated job list.
	var extra struct{}
	err = decoder.Decode(&extra)
	if bounded.N == 0 {
		return nil, fmt.Errorf("jobs response exceeds %d bytes", limit)
	}
	if err != io.EOF {
		return nil, errors.New("jobs response must contain one JSON object")
	}
	seen := make(map[int64]struct{}, len(response.Jobs))
	for _, j := range response.Jobs {
		if j.ID <= 0 || j.ConfigRevision <= 0 {
			return nil, fmt.Errorf("job has no valid configuration revision; upgrade the server and agent together to v2")
		}
		if _, exists := seen[j.ID]; exists {
			return nil, fmt.Errorf("duplicate job for monitor %d", j.ID)
		}
		seen[j.ID] = struct{}{}
	}
	return response.Jobs, nil
}

type resultReceipt struct {
	index     int
	monitorID int64
	checkRun
}

// A batch is acknowledged only when every result is accepted. An oversized
// result is reported and skipped without starving the other monitors. Failures
// leave that batch and subsequent results unacknowledged for fresh checks.
type resultBatches struct {
	ctx           context.Context
	client        *http.Client
	server, token string
	body          []byte
	pending       []resultReceipt
	acknowledge   func(resultReceipt)
	posted        int
	skipped       int
}

func newResultBatches(ctx context.Context, client *http.Client, server, token string, acknowledge func(resultReceipt)) *resultBatches {
	batch := &resultBatches{ctx: ctx, client: client, server: server, token: token,
		body: make([]byte, 1, 64<<10), acknowledge: acknowledge}
	batch.body[0] = '['
	return batch
}

func (b *resultBatches) flush() error {
	if len(b.pending) == 0 {
		return nil
	}
	if err := postResultBatch(b.ctx, b.client, b.server, b.token, append(b.body, ']'), len(b.pending)); err != nil {
		return err
	}
	for _, receipt := range b.pending {
		b.acknowledge(receipt)
	}
	b.posted += len(b.pending)
	b.body = b.body[:1]
	b.pending = b.pending[:0]
	return nil
}

func (b *resultBatches) add(r result, index int) error {
	// JSON may expand each byte by up to six characters. Reject raw values
	// already beyond the budget before allocating their encoded form.
	if len(r.Message)+len(r.DNSRecords)+len(r.BodyHash)+len(r.CertFingerprint)+len(r.Status) > maxResultBatchBytes {
		b.skipped++
		return nil
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded)+2 > maxResultBatchBytes {
		b.skipped++
		return nil
	}
	separator := 0
	if len(b.pending) > 0 {
		separator = 1
	}
	if len(b.body)+separator+len(encoded)+1 > maxResultBatchBytes {
		if err := b.flush(); err != nil {
			return err
		}
		separator = 0
	}
	if separator != 0 {
		b.body = append(b.body, ',')
	}
	b.body = append(b.body, encoded...)
	b.pending = append(b.pending, resultReceipt{index: index, monitorID: r.MonitorID,
		checkRun: checkRun{started: r.CheckedAt, revision: r.ConfigRevision}})
	return nil
}

func (b *resultBatches) finish(postErr error) error {
	if postErr == nil {
		postErr = b.flush()
	}
	if b.skipped > 0 {
		postErr = errors.Join(postErr, fmt.Errorf("%d results could not be encoded within the %d-byte batch limit", b.skipped, maxResultBatchBytes))
	}
	return postErr
}

func postResultBatch(ctx context.Context, client *http.Client, server, token string, body []byte, count int) error {
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var response struct {
		Accepted *int `json:"accepted"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&response); err != nil {
		return fmt.Errorf("decode result acknowledgement: %w", err)
	}
	if response.Accepted == nil || *response.Accepted < 0 || *response.Accepted > count {
		return fmt.Errorf("server returned an invalid result acknowledgement")
	}
	if *response.Accepted != count {
		return fmt.Errorf("server accepted %d of %d results; obsolete jobs are discarded, fetch current jobs on the next poll", *response.Accepted, count)
	}
	return nil
}
