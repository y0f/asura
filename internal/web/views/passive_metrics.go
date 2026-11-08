package views

import (
	"fmt"
	"strconv"
	"time"
)

// HeartbeatData contains display facts only, never enrollment credentials.
type HeartbeatData struct {
	LastPingAt *time.Time
	GraceSecs  int
}

func (p MonitorDetailParams) hasConfiguration() bool {
	return p.Monitor.HasSampledMetrics() || p.Monitor.ResendInterval > 0 || len(p.Tags) > 0 || p.Monitor.Description != ""
}

func (p MonitorFormParams) slaTargetValue() string {
	return strconv.FormatFloat(p.Monitor.SLATarget, 'f', -1, 64)
}

// Preserve an unchanged inactive target. An active monitor with a positive
// target must submit an explicit zero before becoming passive.
func (p MonitorFormParams) inactiveSLADisabledExpression() string {
	if (p.Monitor.ID != 0 && !p.Monitor.HasSampledMetrics()) || p.Monitor.SLATarget == 0 {
		return fmt.Sprintf("(monitorType === 'manual' || monitorType === 'heartbeat') && Number(slaTarget) === %s", p.slaTargetValue())
	}
	return "false"
}
