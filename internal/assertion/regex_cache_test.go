package assertion

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRegexCacheBoundsHistoricalPatternsAndPreservesResults(t *testing.T) {
	var cache regexCache
	for i := range regexCacheCapacity * 3 {
		value := fmt.Sprintf("monitor-%d", i)
		compiled, err := cache.compile("^" + value + "$")
		if err != nil || !compiled.MatchString(value) || compiled.MatchString(value+"extra") {
			t.Fatalf("wrong compiled pattern after edits: %v", err)
		}
	}
	if len(cache.entries) != regexCacheCapacity {
		t.Fatalf("historical patterns exceeded retention bound: %d", len(cache.entries))
	}
	old, err := cache.compile("^monitor-0$")
	if err != nil || !old.MatchString("monitor-0") || len(cache.entries) != regexCacheCapacity {
		t.Fatal("an evicted pattern could not be used again")
	}
	large := strings.Repeat("a", maxCachedPatternBytes+1)
	compiled, err := cache.compile(large)
	if err != nil || !compiled.MatchString(large) || cache.entries[large] != nil {
		t.Fatal("oversized pattern was rejected or retained")
	}
}

func TestRegexCacheConcurrentEditsAndReads(t *testing.T) {
	var cache regexCache
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Go(func() {
			for i := range regexCacheCapacity {
				pattern := fmt.Sprintf("^worker-%d-%d$", worker, i)
				if _, err := cache.compile(pattern); err != nil {
					t.Error(err)
				}
				compiled, err := cache.compile("^shared$")
				if err != nil || !compiled.MatchString("shared") {
					t.Error("concurrent compilation returned another pattern")
				}
			}
		})
	}
	workers.Wait()
	if len(cache.entries) > regexCacheCapacity {
		t.Fatal("concurrent insertion exceeded the cache bound")
	}
}
