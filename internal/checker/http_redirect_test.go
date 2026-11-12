package checker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/v3/internal/model"
)

func TestHTTPRedirectLimitCountsFollowedHops(t *testing.T) {
	followFalse := false
	for _, tc := range []struct {
		name         string
		limit, hops  int
		follow       *bool
		status       int
		wantStatus   string
		wantRequests int64
	}{
		{name: "one allowed hop", limit: 1, hops: 1, status: 200, wantStatus: "up", wantRequests: 2},
		{name: "two allowed hops", limit: 2, hops: 2, status: 200, wantStatus: "up", wantRequests: 3},
		{name: "one extra hop rejected", limit: 2, hops: 3, status: 200, wantStatus: "down", wantRequests: 3},
		{name: "zero uses default ten hops", hops: 10, status: 200, wantStatus: "up", wantRequests: 11},
		{name: "default ceiling enforced", hops: 11, status: 200, wantStatus: "down", wantRequests: 11},
		{name: "following explicitly disabled", hops: 1, follow: &followFalse, status: 302, wantStatus: "up", wantRequests: 1},
		{name: "positive limit takes precedence", limit: 1, hops: 1, follow: &followFalse, status: 200, wantStatus: "up", wantRequests: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				remaining, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
				if err != nil {
					http.Error(w, "invalid fixture path", http.StatusBadRequest)
					return
				}
				if remaining > 0 {
					http.Redirect(w, r, "/"+strconv.Itoa(remaining-1), http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			settings, err := json.Marshal(model.HTTPSettings{
				MaxRedirects: tc.limit, FollowRedirects: tc.follow, ExpectedStatus: tc.status,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (&HTTPChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{
				Target: srv.URL + "/" + strconv.Itoa(tc.hops), Timeout: 3, Settings: settings,
			})
			if err != nil || result == nil || result.Status != tc.wantStatus {
				t.Fatalf("check = %+v, %v; want %s", result, err, tc.wantStatus)
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Errorf("requested %d URLs; want %d including the initial request", got, tc.wantRequests)
			}
		})
	}
}
