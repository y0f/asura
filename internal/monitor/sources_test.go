package monitor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

// Agents before v1.3.1 never report DNS records. A dns_record condition must
// not mark their results down; it is evaluated once records are reported.
func TestAgentDNSRecordAssertion(t *testing.T) {
	assertions := `{"groups":[{"conditions":[{"type":"dns_record","operator":"contains","value":"1.2.3.4"}]}]}`
	tests := []struct {
		name, records, want string
	}{
		{"no records reported", "", "up"},
		{"matching record", `["1.2.3.4"]`, "up"},
		{"no matching record", `["5.6.7.8"]`, "down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mon := &storage.Monitor{Type: "dns", Assertions: json.RawMessage(assertions)}
			cr := &storage.CheckResult{Status: "up", DNSRecords: tt.records}
			if got := evaluateAgentResult(mon, cr, &storage.MonitorStatus{}); got != tt.want {
				t.Fatalf("got %q, want %q (message %q)", got, tt.want, cr.Message)
			}
		})
	}
}

// applyAgent records an agent result unless it is stale, as
// ProcessAgentResult does, and reports whether it was applied.
func applyAgent(p *Pipeline, mon *storage.Monitor, st *storage.MonitorStatus, source, result string, checkedAt, now time.Time) bool {
	if p.staleSourceResult(mon, source, checkedAt) {
		return false
	}
	p.commitSourceResult(mon, p.prepareSourceResult(mon, st, source, result, "", checkedAt, now))
	return true
}

// An agent with a long cycle posts results checked minutes ago. With nothing
// newer from that agent, the result is applied however old it is.
func TestAgentResultLongCycleApplied(t *testing.T) {
	p := &Pipeline{}
	mon := &storage.Monitor{ID: 1, Interval: 30}
	st := &storage.MonitorStatus{MonitorID: 1, Status: "up"}
	now := time.Now()

	if !applyAgent(p, mon, st, "agent:1", "down", now.Add(-10*time.Minute), now) {
		t.Fatal("first result from an agent, checked 10 minutes ago, was treated as stale")
	}
	if st.Status != "down" {
		t.Fatalf("status = %q, want down", st.Status)
	}
	// Its next cycle, again posted long after the check, is applied too.
	later := now.Add(10 * time.Minute)
	if !applyAgent(p, mon, st, "agent:1", "up", later.Add(-9*time.Minute), later) {
		t.Fatal("next long-cycle result from the same agent was treated as stale")
	}
	// Even after the agent was quiet long enough to drop out of the
	// aggregate, an hour-old result with nothing newer from it is applied.
	muchLater := later.Add(2 * time.Hour)
	if !applyAgent(p, mon, st, "agent:2", "up", muchLater.Add(-time.Hour), muchLater) {
		t.Fatal("hour-old first result from another agent was treated as stale")
	}
}

// A result checked before the newest one applied from the same agent is
// dropped, also after the agent was quiet long enough to leave the aggregate.
func TestAgentResultOlderThanSameAgentDropped(t *testing.T) {
	p := &Pipeline{}
	mon := &storage.Monitor{ID: 1, Interval: 30}
	st := &storage.MonitorStatus{MonitorID: 1, Status: "up"}
	now := time.Now()

	applyAgent(p, mon, st, "agent:1", "down", now.Add(-time.Minute), now)
	if applyAgent(p, mon, st, "agent:1", "up", now.Add(-2*time.Minute), now) {
		t.Error("result checked before the agent's newest applied result was applied")
	}
	if st.Status != "down" {
		t.Errorf("status = %q after a stale result, want down", st.Status)
	}

	// Other locations keep reporting while agent:1 is quiet for an hour, so
	// its streak is pruned; its ordering is not forgotten.
	later := now.Add(time.Hour)
	p.commitSourceResult(mon, p.prepareSourceResult(mon, st, localSource, "up", "", later, later))
	if applyAgent(p, mon, st, "agent:1", "down", now.Add(-3*time.Minute), later) {
		t.Error("result older than the agent's newest applied one was applied after its streak was pruned")
	}
	// An equal checked_at is not older, so it is applied.
	if !applyAgent(p, mon, st, "agent:1", "down", now.Add(-time.Minute), later) {
		t.Error("result with the same checked_at as the agent's newest was dropped")
	}
}

// Results from other locations, newer or not, never make a result stale.
func TestAgentResultNotStaleFromOtherSources(t *testing.T) {
	p := &Pipeline{}
	mon := &storage.Monitor{ID: 1, Interval: 30}
	st := &storage.MonitorStatus{MonitorID: 1, Status: "up"}
	now := time.Now()

	p.commitSourceResult(mon, p.prepareSourceResult(mon, st, localSource, "up", "", now, now))
	applyAgent(p, mon, st, "agent:2", "up", now, now)
	if !applyAgent(p, mon, st, "agent:1", "down", now.Add(-5*time.Minute), now) {
		t.Fatal("agent result was dropped because other locations reported more recently")
	}
	if st.Status != "down" {
		t.Errorf("status = %q, want down", st.Status)
	}
}
