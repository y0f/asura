package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/storage"
)

var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeAsura serves jobs and records posted results.
type fakeAsura struct {
	mu      sync.Mutex
	jobs    []byte
	results []result
	posted  chan struct{}
	jobWait time.Duration
}

func newFakeAsura(t *testing.T, jobs any) (*fakeAsura, *httptest.Server) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"jobs": jobs})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAsura{jobs: b, posted: make(chan struct{}, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/jobs":
			f.mu.Lock()
			wait := f.jobWait
			f.mu.Unlock()
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-r.Context().Done():
					return
				}
			}
			w.Write(f.jobs)
		case "/api/v1/agent/results":
			var rs []result
			json.NewDecoder(r.Body).Decode(&rs)
			f.mu.Lock()
			f.results = append(f.results, rs...)
			f.mu.Unlock()
			w.Write([]byte(`{}`))
			f.posted <- struct{}{}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

// Secrets are never sent to agents, so a monitor that needs them must not be
// offered as a job: the agent would report a false "down" for it.
func TestAuthenticatedMonitorNotOfferedToAgents(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer target.Close()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "a.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	authed := &storage.Monitor{
		Name: "auth", Type: "http", Target: target.URL, Interval: 60, Timeout: 5,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"auth_method":"bearer","bearer_token":"s3cret","expected_status":200}`),
	}
	plain := &storage.Monitor{
		Name: "plain", Type: "http", Target: target.URL + "/public", Interval: 60, Timeout: 5,
		Enabled: true, AgentEnabled: true, FailureThreshold: 1, SuccessThreshold: 1,
		Settings: json.RawMessage(`{"auth_method":"none","expected_status":401}`),
	}
	for _, m := range []*storage.Monitor{authed, plain} {
		if err := store.CreateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	jobs, err := store.ListAgentJobs(ctx, 1) // no assignments: every agent gets these jobs
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != plain.ID {
		t.Fatalf("expected only the monitor without secrets as a job, got %+v", jobs)
	}

	f, srv := newFakeAsura(t, jobs)
	a := newAgent(srv.Client(), srv.URL, "tok", checker.DefaultRegistry(nil, true), testLogger)
	a.runCycle(ctx)
	if len(f.results) != 1 {
		t.Fatalf("expected 1 posted result, got %d", len(f.results))
	}
	for _, r := range f.results {
		if r.MonitorID == authed.ID {
			t.Fatalf("agent checked the authenticated monitor: %+v", r)
		}
		if r.Status != "up" {
			t.Fatalf("agent reported %q (code %d, msg %q)", r.Status, r.StatusCode, r.Message)
		}
	}
}

func TestAgentHonoursIntervalAndRunsConcurrently(t *testing.T) {
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	reg := checker.DefaultRegistry(nil, true)
	ctx := context.Background()

	// One hourly job over 4 polls (2 minutes at the default poll interval).
	_, srv := newFakeAsura(t, []job{{ID: 1, Name: "hourly", Type: "http", Target: target.URL, Interval: 3600, Timeout: 5}})
	a := newAgent(srv.Client(), srv.URL, "tok", reg, testLogger)
	clock := time.Now()
	a.now = func() time.Time { return clock }
	for i := 0; i < 4; i++ {
		a.runCycle(ctx)
		clock = clock.Add(30 * time.Second)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("monitor with interval=3600 was checked %d times within 4 polls", n)
	}
	clock = clock.Add(time.Hour)
	a.runCycle(ctx)
	if n := hits.Load(); n != 2 {
		t.Fatalf("monitor should be checked again after its interval, got %d checks", n)
	}

	// Five jobs whose targets each take 300ms run in parallel.
	var five []job
	for i := 1; i <= 5; i++ {
		five = append(five, job{ID: int64(i), Name: "slow", Type: "http", Target: target.URL, Interval: 30, Timeout: 5})
	}
	f5, srv5 := newFakeAsura(t, five)
	t0 := time.Now()
	newAgent(srv5.Client(), srv5.URL, "tok", reg, testLogger).runCycle(ctx)
	if cycle := time.Since(t0); cycle >= 5*300*time.Millisecond {
		t.Fatalf("cycle took %v: jobs ran sequentially", cycle)
	}
	if len(f5.results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(f5.results))
	}
}

// TestHelperAgentMain runs the real main() in a child process.
func TestHelperAgentMain(t *testing.T) {
	if os.Getenv("ASURA_TEST_AGENT_MAIN") != "1" {
		t.Skip("helper process only")
	}
	os.Args = append([]string{"asura-agent"}, strings.Fields(os.Getenv("ASURA_TEST_AGENT_ARGS"))...)
	main()
	os.Exit(0)
}

func startAgentProcess(t *testing.T, args string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperAgentMain$")
	cmd.Env = append(os.Environ(), "ASURA_TEST_AGENT_MAIN=1", "ASURA_TEST_AGENT_ARGS="+args)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

func TestAgentAllowPrivateFlag(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer target.Close()

	for _, tc := range []struct {
		flag string
		want string
	}{
		{"", "down"},
		{"-allow-private", "up"},
	} {
		t.Run("flag="+tc.flag, func(t *testing.T) {
			f, srv := newFakeAsura(t, []job{{ID: 7, Name: "lan", Type: "http", Target: target.URL, Interval: 30, Timeout: 5}})
			startAgentProcess(t, "-server "+srv.URL+" -token tok -interval 1h "+tc.flag)
			select {
			case <-f.posted:
			case <-time.After(20 * time.Second):
				t.Fatal("agent never posted results")
			}
			f.mu.Lock()
			got := f.results[0]
			f.mu.Unlock()
			if got.Status != tc.want {
				t.Fatalf("agent reported %q for loopback target %s, want %q: %q", got.Status, target.URL, tc.want, got.Message)
			}
		})
	}
}

func TestAgentSignalCancelsCycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX SIGTERM delivery")
	}
	f, srv := newFakeAsura(t, []job{})
	f.mu.Lock()
	f.jobWait = 20 * time.Second // the cycle is in flight (fetching jobs) for 20s
	f.mu.Unlock()
	cmd := startAgentProcess(t, "-server "+srv.URL+" -token tok -interval 1h")
	time.Sleep(1 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t0 := time.Now()
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("agent still running %v after SIGTERM: in-flight cycle was not cancelled", time.Since(t0))
	}
}

// fakeDNSChecker reports fixed DNS records, or fails when err is set.
type fakeDNSChecker struct{ err error }

func (fakeDNSChecker) Type() string { return "dns" }

func (c fakeDNSChecker) Check(context.Context, *storage.Monitor) (*checker.Result, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &checker.Result{Status: "up", DNSRecords: []string{"1.2.3.4", "5.6.7.8"}}, nil
}

// The server evaluates dns_record assertions against the reported records and
// drops results older than its latest check using checked_at, so every posted
// result must carry both.
func TestAgentPostsDNSRecordsAndCheckedAt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		checker     fakeDNSChecker
		wantRecords any
	}{
		{"check succeeded", fakeDNSChecker{}, []any{"1.2.3.4", "5.6.7.8"}},
		{"check failed", fakeDNSChecker{err: io.ErrUnexpectedEOF}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posted []map[string]any
			var postedAt time.Time
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/agent/jobs":
					json.NewEncoder(w).Encode(map[string]any{"jobs": []job{{ID: 3, Name: "dns", Type: "dns", Target: "example.com", Interval: 30, Timeout: 5}}})
				case "/api/v1/agent/results":
					postedAt = time.Now()
					json.NewDecoder(r.Body).Decode(&posted)
					w.Write([]byte(`{}`))
				}
			}))
			defer srv.Close()

			reg := checker.NewRegistry()
			reg.Register(tc.checker)
			before := time.Now()
			newAgent(srv.Client(), srv.URL, "tok", reg, testLogger).runCycle(context.Background())
			if len(posted) != 1 {
				t.Fatalf("expected 1 posted result, got %d", len(posted))
			}
			got := posted[0]

			var records any
			if s, _ := got["dns_records"].(string); s != "" {
				if err := json.Unmarshal([]byte(s), &records); err != nil {
					t.Fatalf("dns_records %q is not a JSON array: %v", s, err)
				}
			}
			if !reflect.DeepEqual(records, tc.wantRecords) {
				t.Fatalf("dns_records = %v, want %v", records, tc.wantRecords)
			}

			s, _ := got["checked_at"].(string)
			checkedAt, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				t.Fatalf("checked_at %q: %v", s, err)
			}
			if checkedAt.Before(before) || checkedAt.After(postedAt) {
				t.Fatalf("checked_at %v not between cycle start %v and post %v", checkedAt, before, postedAt)
			}
		})
	}
}
