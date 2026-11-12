package views

import "github.com/y0f/asura/v3/internal/storage"

type checkHistoryColumns struct {
	Response, Code, Certificate bool
}

func (p MonitorDetailParams) historyColumns() checkHistoryColumns {
	var columns checkHistoryColumns
	for _, check := range p.checks() {
		if check.ObservationKind != storage.ObservationProbe {
			continue
		}
		columns.Response = true
		columns.Code = columns.Code || check.StatusCode != 0
		columns.Certificate = columns.Certificate || check.CertExpiry != nil
	}
	return columns
}

func observationLabel(kind string) string {
	switch kind {
	case storage.ObservationProbe:
		return "Probe"
	case storage.ObservationManual:
		return "Manual state"
	case storage.ObservationHeartbeat:
		return "Heartbeat state"
	default:
		return "Legacy observation"
	}
}
