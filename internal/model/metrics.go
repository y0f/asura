package model

// Manual transitions and heartbeat arrivals are not scheduled probe samples.
// Their state history cannot be interpreted as sample-based uptime or latency.
func (m *Monitor) HasSampledMetrics() bool {
	return m != nil && m.Type != "manual" && m.Type != "heartbeat"
}
