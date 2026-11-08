package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Exercise the production streaming batcher with a finite collection.
func postResults(ctx context.Context, client *http.Client, server, token string, results []result) error {
	_, err := postResultsBatched(ctx, client, server, token, results)
	return err
}

func postResultsBatched(ctx context.Context, client *http.Client, server, token string, results []result) ([]bool, error) {
	acknowledged := make([]bool, len(results))
	batch := newResultBatches(ctx, client, server, token, func(receipt resultReceipt) {
		acknowledged[receipt.index] = true
	})
	var err error
	for i, r := range results {
		if err = batch.add(r, i); err != nil {
			break
		}
	}
	return acknowledged, batch.finish(err)
}

func TestAgentBatchAcknowledgementsAndOversizedIsolation(t *testing.T) {
	for _, scenario := range []string{"complete", "http failure", "partial acceptance", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var received []int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch []result
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, r := range batch {
					received = append(received, r.MonitorID)
				}
				if len(received) == 2 {
					switch scenario {
					case "http failure":
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					case "partial acceptance":
						json.NewEncoder(w).Encode(map[string]int{"accepted": 0})
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]int{"accepted": len(batch)})
			}))
			defer srv.Close()
			message := strings.Repeat("x", 4<<20)
			results := []result{{MonitorID: 1, Message: message}, {MonitorID: 2, Message: message}, {MonitorID: 3, Message: message}}
			wantAck := []bool{true, true, true}
			wantIDs := []int64{1, 2, 3}
			switch scenario {
			case "http failure", "partial acceptance":
				wantAck, wantIDs = []bool{true, false, false}, []int64{1, 2}
			case "oversized":
				// Raw text fits the limit, but escaped JSON exceeds it.
				results[1].Message = strings.Repeat("\x00", 2<<20)
				wantAck, wantIDs = []bool{true, false, true}, []int64{1, 3}
			}
			ack, err := postResultsBatched(context.Background(), srv.Client(), srv.URL, "fixture", results)
			mu.Lock()
			defer mu.Unlock()
			if (err == nil) != (scenario == "complete") || !reflect.DeepEqual(ack, wantAck) || !reflect.DeepEqual(received, wantIDs) {
				t.Fatalf("ack=%v received=%v err=%v; want ack=%v received=%v", ack, received, err, wantAck, wantIDs)
			}
		})
	}
}

func TestDecodeJobsBoundsAndEnvelope(t *testing.T) {
	valid := `{"jobs":[{"id":1,"config_revision":2}]}`
	for _, tc := range []struct {
		name  string
		body  string
		limit int64
		ok    bool
	}{
		{"exact limit", valid, int64(len(valid)), true},
		{"over limit", valid, int64(len(valid) - 1), false},
		{"trailing whitespace", valid + " \n", 100, true},
		{"trailing data exceeds limit", valid + strings.Repeat(" ", 100), 100, false},
		{"second envelope", valid + valid, 100, false},
		{"null envelope", "null", 100, false},
		{"trailing garbage", valid + "x", 100, false},
		{"duplicate jobs", `{"jobs":[{"id":1,"config_revision":2},{"id":1,"config_revision":2}]}`, 100, false},
		{"truncated response", valid[:len(valid)-1], 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs, err := decodeJobs(strings.NewReader(tc.body), tc.limit)
			if (err == nil) != tc.ok || (tc.ok && (len(jobs) != 1 || jobs[0].ConfigRevision != 2)) {
				t.Fatalf("jobs=%v err=%v", jobs, err)
			}
		})
	}
	if jobs, err := decodeJobs(strings.NewReader(`{"jobs":null}`), 100); err != nil || len(jobs) != 0 {
		t.Fatalf("empty server job list was rejected: %v %v", jobs, err)
	}
}
