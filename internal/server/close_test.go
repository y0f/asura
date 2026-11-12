package server

import (
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/notifier"
)

// Close stops the goroutines NewServer starts (rate limiter cleanups,
// including the login limiter shared by the API and the web UI), so building
// and closing servers does not leak them.
func TestServerCloseStopsBackgroundGoroutines(t *testing.T) {
	for _, webUI := range []bool{true, false} {
		srv, store := regressionServer(t, nil)
		srv.Close()
		cfg := srv.cfg
		cfg.Server.WebUIEnabled = &webUI
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		dispatcher := notifier.NewDispatcher(store, logger, true)
		newAndClose := func(n int) {
			for range n {
				NewServer(cfg, store, nil, dispatcher, nil, logger, "test").Close()
			}
		}

		newAndClose(3)
		time.Sleep(50 * time.Millisecond)
		base := runtime.NumGoroutine()
		const servers = 50
		newAndClose(servers)
		deadline := time.Now().Add(2 * time.Second)
		n := runtime.NumGoroutine()
		for n > base+servers/10 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			n = runtime.NumGoroutine()
		}
		if n > base+servers/10 {
			t.Errorf("web UI %v: %d goroutines after creating and closing %d servers, %d before", webUI, n, servers, base)
		}
	}
}
