package views

import "fmt"

// PermissionChecker evaluates both global and resource-scoped grants. Views
// receive the validated request identity through this small interface.
type PermissionChecker interface {
	HasCapability(string) bool
	AllowsMonitor(string, int64, *int64) bool
}

func (p LayoutParams) HasCapability(action string) bool {
	if p.Access != nil {
		return p.Access.HasCapability(action)
	}
	return p.Can(action)
}

func (p LayoutParams) CanMonitor(action string, monitorID int64, groupID *int64) bool {
	if p.Access != nil {
		return p.Access.AllowsMonitor(action, monitorID, groupID)
	}
	return p.Can(action)
}

func (p LayoutParams) MonitorLabel(name string, monitorID int64, groupID *int64) string {
	if name != "" && p.CanMonitor("monitors.read", monitorID, groupID) {
		return name
	}
	return fmt.Sprintf("Monitor #%d", monitorID)
}
