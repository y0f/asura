package validate

import (
	"errors"

	"github.com/y0f/asura/v3/internal/model"
)

// Old passive targets remain portable metadata, but must not become active
// sample-based promises. Ordinary edits may preserve the unchanged legacy value.
func ValidateMonitorSLAChange(next, previous *model.Monitor) error {
	if next.HasSampledMetrics() || next.SLATarget == 0 {
		return nil
	}
	if previous != nil && previous.Type == next.Type && previous.SLATarget == next.SLATarget {
		return nil
	}
	return errors.New("sample-based SLA targets are unavailable for manual and heartbeat monitors; clear the target to continue")
}
