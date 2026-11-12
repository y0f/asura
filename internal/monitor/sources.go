package monitor

import (
	"encoding/json"
	"maps"
	"strconv"
	"time"

	"github.com/y0f/asura/v3/internal/assertion"
	"github.com/y0f/asura/v3/internal/checker"
	"github.com/y0f/asura/v3/internal/storage"
)

// A monitor can be checked from several locations: the local worker and any
// number of remote agents. Each location keeps its own consecutive
// fail/success streak, so one location cannot reset another's streak and make
// the monitor flap. The persisted monitor_status holds the aggregate:
//
//   - the monitor is not up while any fresh location reports a failure; its
//     status is the worst one reported and ConsecFails the longest failing
//     streak, so a regional outage still reaches the failure threshold;
//   - it is up once every fresh location reports up, with ConsecSuccesses the
//     shortest success streak, so recovery waits for all locations.
//
// Locations that stopped reporting are dropped after sourceStaleAfter.

const localSource = "local"

type sourceState struct {
	status    string
	message   string
	fails     int
	successes int
	at        time.Time // when the result was processed
}

type monitorSources map[string]*sourceState

func agentSource(cr *storage.CheckResult) string {
	if cr.AgentID == nil {
		return "agent"
	}
	return "agent:" + strconv.FormatInt(*cr.AgentID, 10)
}

func sourceStaleAfter(mon *storage.Monitor) time.Duration {
	d := 3 * time.Duration(mon.Interval) * time.Second
	return max(d, 2*time.Minute)
}

func statusSeverity(status string) int {
	switch status {
	case "up":
		return 0
	case "degraded":
		return 1
	default:
		return 2
	}
}

// staleSourceResult reports whether a result source checked at checkedAt must
// not drive the monitor's status because it was checked before the newest
// result already applied from that same source. Staleness is judged only per
// location and only by order: results from other locations say nothing about
// the order of this one's observations, and a result that took long to arrive
// (an agent posting at the end of a long cycle) is not stale by age alone.
// The caller must hold the monitor's status lock.
func (p *Pipeline) staleSourceResult(mon *storage.Monitor, source string, checkedAt time.Time) bool {
	if revision, ok := p.sourceRevision.Load(mon.ID); !ok || revision.(int64) != mon.ConfigRevision {
		return false
	}
	v, ok := p.sourceCheckedAt.Load(mon.ID)
	if !ok {
		return false
	}
	last, ok := v.(map[string]time.Time)[source]
	return ok && checkedAt.Before(last)
}

type preparedSourceResult struct {
	sources   monitorSources
	checkedAt map[string]time.Time
	message   string
}

// prepareSourceResult computes the next aggregate without publishing cache
// changes. The caller holds the monitor lock and commits this preparation only
// after its database transaction succeeds. A new revision discards observations
// and ordering from old configurations, including revoked agent assignments.
func (p *Pipeline) prepareSourceResult(mon *storage.Monitor, status *storage.MonitorStatus, source, result, message string, checkedAt, now time.Time) preparedSourceResult {
	prepared := preparedSourceResult{sources: monitorSources{}, checkedAt: map[string]time.Time{}, message: message}
	if revision, ok := p.sourceRevision.Load(mon.ID); ok && revision.(int64) == mon.ConfigRevision {
		if v, ok := p.sources.Load(mon.ID); ok {
			prepared.sources = maps.Clone(v.(monitorSources))
		}
		if v, ok := p.sourceCheckedAt.Load(mon.ID); ok {
			prepared.checkedAt = maps.Clone(v.(map[string]time.Time))
		}
	}
	if checkedAt.After(prepared.checkedAt[source]) {
		prepared.checkedAt[source] = checkedAt
	}
	srcs := prepared.sources

	stale := sourceStaleAfter(mon)
	for k, s := range srcs {
		if k != source && now.Sub(s.at) > stale {
			delete(srcs, k)
		}
	}

	st := srcs[source]
	if st == nil {
		st = &sourceState{}
		if len(srcs) == 0 {
			// First location seen since startup (or all others went stale):
			// continue from the persisted streak.
			st.fails, st.successes = status.ConsecFails, status.ConsecSuccesses
		}
	} else {
		copy := *st
		st = &copy
	}
	srcs[source] = st
	st.status, st.message, st.at = result, message, now
	if result == "up" {
		st.successes++
		st.fails = 0
	} else {
		st.fails++
		st.successes = 0
	}

	var worst *sourceState
	minSuccesses := st.successes
	for _, s := range srcs {
		if s.status == "up" {
			minSuccesses = min(minSuccesses, s.successes)
			continue
		}
		if worst == nil || statusSeverity(s.status) > statusSeverity(worst.status) ||
			(s.status == worst.status && s.fails > worst.fails) {
			worst = s
		}
	}

	if worst == nil {
		status.Status = "up"
		status.ConsecSuccesses = minSuccesses
		status.ConsecFails = 0
		return prepared
	}
	maxFails := 0
	for _, s := range srcs {
		if s.status != "up" {
			maxFails = max(maxFails, s.fails)
		}
	}
	status.Status = worst.status
	status.ConsecFails = maxFails
	status.ConsecSuccesses = 0
	prepared.message = worst.message
	return prepared
}

func (p *Pipeline) commitSourceResult(mon *storage.Monitor, prepared preparedSourceResult) {
	p.sources.Store(mon.ID, prepared.sources)
	p.sourceCheckedAt.Store(mon.ID, prepared.checkedAt)
	p.sourceRevision.Store(mon.ID, mon.ConfigRevision)
}

// evaluateAgentResult applies assertions, upside_down and anomaly detection
// to a result reported by a remote agent, updating cr.Status and cr.Message.
func evaluateAgentResult(mon *storage.Monitor, cr *storage.CheckResult, status *storage.MonitorStatus) string {
	result := &checker.Result{
		Status:       cr.Status,
		ResponseTime: cr.ResponseTime,
		StatusCode:   cr.StatusCode,
		Message:      cr.Message,
	}
	hasDNSRecords := cr.DNSRecords != ""
	if hasDNSRecords {
		_ = json.Unmarshal([]byte(cr.DNSRecords), &result.DNSRecords)
	}

	finalStatus := applyUpsideDown(mon, evaluateAssertionSet(agentAssertions(mon.Assertions, hasDNSRecords), result), result)
	if finalStatus == "up" {
		if msg, ok := anomalyMessage(mon, status, result.ResponseTime); ok {
			finalStatus = "degraded"
			result.Message = msg
		}
	}

	cr.Status = finalStatus
	cr.Message = result.Message
	return finalStatus
}

// agentEvaluable lists the assertion types whose inputs agents report. Agents
// send no body, headers or certificate expiry, and agents older than v1.3.1
// send no DNS records either.
var agentEvaluable = map[string]bool{
	"status_code":   true,
	"response_time": true,
	"dns_record":    true,
}

// agentAssertions reduces an assertion set to what can be evaluated from an
// agent result. Conditions an agent cannot evaluate are treated as passing,
// so a missing body never fails a check on its own. dns_record conditions are
// only evaluated when the agent reported records (hasDNSRecords).
func agentAssertions(raw json.RawMessage, hasDNSRecords bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var cs assertion.ConditionSet
	if err := json.Unmarshal(raw, &cs); err != nil {
		return nil
	}

	var groups []assertion.ConditionGroup
	for _, g := range cs.Groups {
		var conds []assertion.Assertion
		passes := len(g.Conditions) == 0
		for _, c := range g.Conditions {
			if agentEvaluable[c.Type] && (c.Type != "dns_record" || hasDNSRecords) {
				conds = append(conds, c)
			} else if g.Operator == "or" {
				passes = true
			}
		}
		if passes || len(conds) == 0 {
			// The group passes regardless of the evaluable conditions.
			if cs.Operator == "or" {
				return nil
			}
			continue
		}
		g.Conditions = conds
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		return nil
	}
	cs.Groups = groups
	out, err := json.Marshal(cs)
	if err != nil {
		return nil
	}
	return out
}
