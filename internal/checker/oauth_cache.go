package checker

import (
	"container/list"
	"sync"
)

// Deleted monitors and agents may never receive a web cache-clear signal.
// Bound retained entries and token payloads independently of monitor churn.
const (
	maxOAuthCacheEntries     = 256
	maxCachedOAuthTokenBytes = 16 << 10
)

type oauthTokenCache struct {
	mu      sync.Mutex
	entries map[int64]*list.Element
	recent  list.List
}

type oauthCacheEntry struct {
	monitorID int64
	token     *oauth2CachedToken
}

func (c *oauthTokenCache) get(monitorID int64) *oauth2CachedToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[monitorID]; element != nil {
		c.recent.MoveToFront(element)
		return element.Value.(oauthCacheEntry).token
	}
	if c.entries == nil {
		c.entries = make(map[int64]*list.Element)
	}
	token := &oauth2CachedToken{}
	c.entries[monitorID] = c.recent.PushFront(oauthCacheEntry{monitorID, token})
	if len(c.entries) > maxOAuthCacheEntries {
		oldest := c.recent.Back()
		delete(c.entries, oldest.Value.(oauthCacheEntry).monitorID)
		c.recent.Remove(oldest)
	}
	return token
}

func (c *oauthTokenCache) remove(monitorID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[monitorID]; element != nil {
		delete(c.entries, monitorID)
		c.recent.Remove(element)
	}
}
