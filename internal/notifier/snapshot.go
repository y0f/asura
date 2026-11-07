package notifier

import (
	"slices"

	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
)

func copyPointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

// Snapshot before returning to a producer that may reuse its objects. Delivery
// needs channel credentials, but never monitor credentials, proxies or full
// response bodies. Each channel receives its own snapshot, including retries.
func snapshotPayload(p *Payload) *Payload {
	copy := *p
	copy.Incident = copyPointer(p.Incident)
	if inc := copy.Incident; inc != nil {
		inc.AcknowledgedAt = copyPointer(inc.AcknowledgedAt)
		inc.ResolvedAt = copyPointer(inc.ResolvedAt)
	}
	copy.Monitor = redact.Monitor(p.Monitor)
	if mon := copy.Monitor; mon != nil {
		mon.ProxyURL = ""
		mon.Tags = slices.Clone(mon.Tags)
		mon.Assertions = slices.Clone(mon.Assertions)
		mon.NotificationChannelIDs = slices.Clone(mon.NotificationChannelIDs)
		mon.MonitorTags = slices.Clone(mon.MonitorTags)
		mon.AgentIDs = slices.Clone(mon.AgentIDs)
		mon.GroupID = copyPointer(mon.GroupID)
		mon.ProxyID = copyPointer(mon.ProxyID)
		mon.EscalationPolicyID = copyPointer(mon.EscalationPolicyID)
		mon.LastCheckAt = copyPointer(mon.LastCheckAt)
	}
	copy.Change = copyPointer(p.Change)
	if copy.Change != nil {
		copy.Change.OldBody, copy.Change.NewBody = "", ""
	}
	return &copy
}

func snapshotChannel(ch *storage.NotificationChannel) *storage.NotificationChannel {
	copy := *ch
	copy.Settings = slices.Clone(ch.Settings)
	copy.Events = slices.Clone(ch.Events)
	return &copy
}
