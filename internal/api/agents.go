package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

var agentEligibleTypes = map[string]bool{
	"http": true, "http_multi": true, "tcp": true, "dns": true, "icmp": true,
	"tls": true, "websocket": true, "domain": true,
	"grpc": true, "mqtt": true, "smtp": true, "ssh": true,
	"redis": true, "postgresql": true, "udp": true,
}

var validCheckStatuses = map[string]bool{
	"up": true, "down": true, "degraded": true,
}

const agentResultMaxBody = 10 << 20 // 10MB

func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		h.logger.Error("list agents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": agents})
}

func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	var a storage.Agent
	if err := h.readJSON(r, &a); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	a.Enabled = true
	if err := h.store.CreateAgent(r.Context(), &a); err != nil {
		h.logger.Error("create agent", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create agent")
		return
	}
	h.audit(r, "create", "agent", a.ID, a.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "name": a.Name, "token": a.Token})
}

func (h *Handler) DeleteAgentAPI(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	disabled, err := h.store.DeleteAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	h.audit(r, "delete", "agent", id, "")
	for _, monID := range disabled {
		h.logger.Warn("monitor disabled for agents: its only assigned agent was deleted", "monitor_id", monID, "agent_id", id)
		h.audit(r, "update", "monitor", monID, fmt.Sprintf("agent checks disabled: only assigned agent %d was deleted", id))
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "agent_disabled_monitor_ids": disabled})
}

func (h *Handler) AgentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Agent-Token")
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing agent token")
			return
		}
		agent, err := h.store.GetAgentByToken(r.Context(), token)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusUnauthorized, "invalid agent token")
				return
			}
			writeError(w, http.StatusInternalServerError, "agent auth failed")
			return
		}
		if !agent.Enabled {
			writeError(w, http.StatusForbidden, "agent is disabled")
			return
		}
		r = r.WithContext(setAgentCtx(r.Context(), agent))
		next.ServeHTTP(w, r)
	})
}

type agentCtxKey struct{}

func setAgentCtx(ctx context.Context, a *storage.Agent) context.Context {
	return context.WithValue(ctx, agentCtxKey{}, a)
}

func getAgentFromCtx(ctx context.Context) *storage.Agent {
	a, _ := ctx.Value(agentCtxKey{}).(*storage.Agent)
	return a
}

func (h *Handler) AgentGetJobs(w http.ResponseWriter, r *http.Request) {
	agent := getAgentFromCtx(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "no agent context")
		return
	}

	if err := h.store.UpdateAgentHeartbeat(r.Context(), agent.ID); err != nil {
		h.logger.Error("update agent heartbeat", "error", err)
	}

	jobs, err := h.store.ListAgentJobs(r.Context(), agent.ID)
	if err != nil {
		h.logger.Error("list agent jobs", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agent.ID,
		"jobs":     jobs,
	})
}

type agentResultRequest struct {
	ConfigRevision  int64  `json:"config_revision"`
	MonitorID       int64  `json:"monitor_id"`
	Status          string `json:"status"`
	ResponseTime    int64  `json:"response_time"`
	StatusCode      int    `json:"status_code"`
	Message         string `json:"message"`
	BodyHash        string `json:"body_hash"`
	CertFingerprint string `json:"cert_fingerprint"`
	DNSRecords      string `json:"dns_records"`
	// CheckedAt is when the agent ran the check. Optional; results are
	// batched per cycle, so the receive time can be minutes later.
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// agentResultMonitor rejects obsolete jobs before evaluation. The persistence
// transaction repeats revision, enabled, type, settings and authenticated-agent
// assignment checks, so edits or revocations after this lookup cannot admit data.
func (h *Handler) agentResultMonitor(ctx context.Context, res agentResultRequest) (*storage.Monitor, error) {
	mon, err := h.store.GetMonitor(ctx, res.MonitorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrStaleMonitorCheck
	}
	if err != nil {
		return nil, err
	}
	if mon == nil || mon.ConfigRevision != res.ConfigRevision || !mon.Enabled || !mon.AgentEnabled || !agentEligibleTypes[mon.Type] {
		return nil, storage.ErrStaleMonitorCheck
	}
	return mon, nil
}

// agentCheckedAt returns the reported checked_at, or the zero time (meaning
// "now", the receive time) when it is missing or in the future, so a skewed
// agent clock cannot make its later results look stale. The pipeline only
// drops a result checked before the newest one applied from the same agent.
func agentCheckedAt(reported *time.Time, now time.Time) time.Time {
	if reported == nil || reported.After(now) {
		return time.Time{}
	}
	return reported.UTC()
}

func (h *Handler) AgentPostResults(w http.ResponseWriter, r *http.Request) {
	agent := getAgentFromCtx(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "no agent context")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, agentResultMaxBody)
	defer r.Body.Close()

	var results []agentResultRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	if err := json.Unmarshal(body, &results); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: expected array of results")
		return
	}

	// Posting results is proof of life too: a long check cycle must not make
	// an active agent look offline.
	if err := h.store.UpdateAgentHeartbeat(r.Context(), agent.ID); err != nil {
		h.logger.Error("update agent heartbeat", "error", err)
	}

	now := time.Now().UTC()
	accepted, stale, failed := 0, 0, 0

	for _, res := range results {
		if res.MonitorID <= 0 || res.ConfigRevision <= 0 || !validCheckStatuses[res.Status] {
			continue
		}
		mon, err := h.agentResultMonitor(r.Context(), res)
		if err != nil {
			if errors.Is(err, storage.ErrStaleMonitorCheck) {
				stale++
			} else {
				failed++
				h.logger.Error("load agent check monitor", "agent_id", agent.ID, "monitor_id", res.MonitorID, "error", err)
			}
			continue
		}

		cr := &storage.CheckResult{
			MonitorID:       res.MonitorID,
			Status:          res.Status,
			ResponseTime:    res.ResponseTime,
			StatusCode:      res.StatusCode,
			Message:         res.Message,
			BodyHash:        res.BodyHash,
			CertFingerprint: res.CertFingerprint,
			DNSRecords:      res.DNSRecords,
			AgentID:         &agent.ID,
			CreatedAt:       agentCheckedAt(res.CheckedAt, now),
		}

		// The pipeline evaluates the result (assertions, upside_down, anomaly)
		// before storing it. An older observation of the same configuration
		// stays in history without changing status; an obsolete job is rejected.
		if h.pipeline != nil {
			err = h.pipeline.ProcessAgentResult(r.Context(), mon, cr)
		} else {
			err = h.store.RecordMonitorCheck(r.Context(), mon, cr, nil)
		}
		if err != nil {
			if errors.Is(err, storage.ErrStaleMonitorCheck) {
				stale++
			} else {
				failed++
				h.logger.Error("insert agent check result", "agent", agent.Name, "monitor_id", res.MonitorID, "error", err)
			}
			continue
		}

		accepted++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": accepted,
		"rejected": len(results) - accepted,
		"stale":    stale,
		"failed":   failed,
		"agent":    agent.Name,
		"time":     now.Format(time.RFC3339),
	})
}

func (h *Handler) AgentHealthAPI(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	type agentStatus struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
		Online   bool   `json:"online"`
		LastSeen string `json:"last_seen,omitempty"`
	}

	now := time.Now().UTC()
	var status []agentStatus
	for _, a := range agents {
		s := agentStatus{ID: a.ID, Name: a.Name, Location: a.Location}
		if a.LastHeartbeat != nil {
			s.Online = now.Sub(*a.LastHeartbeat) < 2*time.Minute
			s.LastSeen = fmt.Sprintf("%ds ago", int(now.Sub(*a.LastHeartbeat).Seconds()))
		}
		status = append(status, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": status})
}
