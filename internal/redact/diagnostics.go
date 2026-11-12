package redact

import "github.com/y0f/asura/v3/internal/storage"

// Check exposes measured scalars without response data, arbitrary checker
// errors or content fingerprints. Agent labels have independent authority.
func Check(check *storage.CheckResult, diagnostics, agentNames bool) *storage.CheckResult {
	if check == nil {
		return nil
	}
	copy := *check
	if !diagnostics {
		copy.Message, copy.Headers, copy.Body, copy.BodyHash = "", "", "", ""
		copy.CertFingerprint, copy.DNSRecords = "", ""
	}
	if !agentNames {
		copy.AgentName = ""
	}
	return &copy
}

func ContentChange(change *storage.ContentChange, diagnostics bool) *storage.ContentChange {
	if change == nil {
		return nil
	}
	copy := *change
	if !diagnostics {
		copy.OldHash, copy.NewHash, copy.Diff = "", "", ""
		copy.OldBody, copy.NewBody = "", ""
	}
	return &copy
}

// IncidentCause is a fixed summary, never a transformation of raw checker
// output. All automatic deliveries use it regardless of recipient grants.
func IncidentCause() string { return "Monitor check failed" }

func IncidentEvent(event *storage.IncidentEvent, diagnostics bool) *storage.IncidentEvent {
	if event == nil {
		return nil
	}
	copy := *event
	if !diagnostics {
		switch event.Type {
		case "created":
			copy.Message = "Incident opened"
		case "acknowledged":
			copy.Message = "Incident acknowledged"
		case "resolved", "check_recovered":
			copy.Message = "Incident resolved"
		case "check_failed":
			copy.Message = IncidentCause()
		case "severity_changed":
			copy.Message = "Incident severity changed"
		case "escalated":
			copy.Message = "Incident escalated"
		default:
			copy.Message = "Incident updated"
		}
	}
	return &copy
}
