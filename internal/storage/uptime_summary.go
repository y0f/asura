package storage

// SummarizeUptime weights uptime by check counts, preserving an empty window as
// zero observations. Callers can distinguish missing data from measured uptime.
func SummarizeUptime(days []*DailyUptime) (percent float64, total int64) {
	var up int64
	for _, day := range days {
		if day == nil {
			continue
		}
		total += day.TotalChecks
		up += day.UpChecks
	}
	if total > 0 {
		percent = float64(up) / float64(total) * 100
	}
	return percent, total
}
