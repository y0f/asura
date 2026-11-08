package assertion

import (
	"regexp"
	"sync"
)

const regexCacheCapacity = 256
const maxCachedPatternBytes = 4096

// A bounded insertion-order cache avoids retaining every historical monitor
// pattern for the server's lifetime. Oversized patterns remain supported but
// are not retained. Compilation happens outside the lock.
type regexCache struct {
	mu      sync.RWMutex
	entries map[string]*regexp.Regexp
	order   [regexCacheCapacity]string
	next    int
}

var compiledPatterns regexCache

func compileRegex(pattern string) (*regexp.Regexp, error) {
	return compiledPatterns.compile(pattern)
}

func (c *regexCache) compile(pattern string) (*regexp.Regexp, error) {
	c.mu.RLock()
	compiled := c.entries[pattern]
	c.mu.RUnlock()
	if compiled != nil {
		return compiled, nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil || len(pattern) > maxCachedPatternBytes {
		return compiled, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[pattern]; existing != nil {
		return existing, nil
	}
	if c.entries == nil {
		c.entries = make(map[string]*regexp.Regexp, regexCacheCapacity)
	}
	if len(c.entries) == regexCacheCapacity {
		delete(c.entries, c.order[c.next])
	}
	c.entries[pattern] = compiled
	c.order[c.next] = pattern
	c.next = (c.next + 1) % regexCacheCapacity
	return compiled, nil
}
