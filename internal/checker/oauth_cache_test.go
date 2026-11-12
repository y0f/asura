package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/y0f/asura/v3/internal/model"
)

func TestOAuthCacheChurnKeepsBoundAndActiveEntries(t *testing.T) {
	var cache oauthTokenCache
	active := cache.get(1)
	active.token = "still active"
	for id := int64(2); id < 4*maxOAuthCacheEntries; id++ {
		cache.get(id)
		if cache.get(1) != active {
			t.Fatal("frequent monitor evicted before inactive entries")
		}
	}
	if len(cache.entries) != maxOAuthCacheEntries || cache.recent.Len() != maxOAuthCacheEntries {
		t.Fatal("cache grows with deleted monitors")
	}
	cache.remove(1)
	if next := cache.get(1); next == active || next.token != "" {
		t.Fatal("explicit invalidation reused an obsolete entry")
	}
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Go(func() {
			for id := range 1000 {
				key := int64(worker*1000 + id)
				cache.get(key)
				cache.remove(key - 5)
			}
		})
	}
	workers.Wait()
	if len(cache.entries) > maxOAuthCacheEntries || cache.recent.Len() != len(cache.entries) {
		t.Fatal("concurrent cache corruption")
	}
}

func TestOAuthOversizedTokenIsUsableWithoutRetention(t *testing.T) {
	token := strings.Repeat("t", maxCachedOAuthTokenBytes+1)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":3600}`, token)
	}))
	defer server.Close()
	m := &model.Monitor{ID: 7002}
	defer ClearOAuth2TokenCache(m.ID)
	for range 2 {
		got, err := fetchOAuth2Token(context.Background(), m, model.HTTPSettings{OAuth2TokenURL: server.URL}, true)
		if err != nil || got != token {
			t.Fatalf("large valid token rejected: %v", err)
		}
	}
	if requests.Load() != 2 || oauth2TokenCache.get(m.ID).token != "" {
		t.Fatal("oversized token retained in cache")
	}
}
