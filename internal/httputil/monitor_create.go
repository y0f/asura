package httputil

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

// MonitorCreateOptions prepares a one-time credential before opening a writer
// transaction. Only the successful create response may reveal the plain token.
func MonitorCreateOptions(m *storage.Monitor) (storage.MonitorCreateOptions, error) {
	options := storage.MonitorCreateOptions{}
	if m.Type != "heartbeat" {
		return options, nil
	}
	grace, err := heartbeatGrace(m.Settings)
	if err != nil {
		return options, err
	}
	var token [16]byte
	if _, err := io.ReadFull(rand.Reader, token[:]); err != nil {
		return options, fmt.Errorf("generate heartbeat credential: %w", err)
	}
	options.Heartbeat = &storage.Heartbeat{Token: hex.EncodeToString(token[:]), Grace: grace, Status: "pending"}
	return options, nil
}

// PrepareMonitorClone uses the current transactional source and a credential
// generated beforehand. Cloning does not implicitly delegate related resources.
func PrepareMonitorClone(key *config.APIKeyConfig, source *storage.Monitor, token string) (*storage.Monitor, storage.MonitorCreateOptions, error) {
	clone := *source
	clone.ID, clone.Name, clone.Enabled = 0, source.Name+" (copy)", false
	clone.AgentEnabled = source.AgentEnabled && len(source.AgentIDs) > 0
	options := storage.MonitorCreateOptions{}
	_, err := PrepareMonitorRelations(key, &clone, nil, storage.MonitorRelationChanges{Group: true, Proxy: true, EscalationPolicy: true, Notifications: true, Tags: true, Agents: true})
	if err != nil {
		return nil, options, err
	}
	if clone.Type == "heartbeat" {
		grace, err := heartbeatGrace(clone.Settings)
		if err != nil {
			return nil, options, err
		}
		options.Heartbeat = &storage.Heartbeat{Token: token, Grace: grace, Status: "pending"}
	}
	return &clone, options, nil
}

func heartbeatGrace(raw json.RawMessage) (int, error) {
	var settings struct {
		Grace int `json:"grace"`
	}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return 0, fmt.Errorf("heartbeat grace must be a nonnegative integer")
		}
	}
	if settings.Grace < 0 {
		return 0, fmt.Errorf("heartbeat grace must be a nonnegative integer")
	}
	return settings.Grace, nil
}
