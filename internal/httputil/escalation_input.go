package httputil

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/validate"
)

var ErrEscalationChannelPermission = errors.New("notification read access is required to assign escalation channels")
var ErrInvalidEscalationPolicy = errors.New("invalid escalation policy")

type EscalationPolicyInput struct {
	// Immutable fields are accepted for a GET/edit/PUT round trip and ignored.
	ID          int64                            `json:"id"`
	CreatedAt   time.Time                        `json:"created_at"`
	UpdatedAt   time.Time                        `json:"updated_at"`
	Name        *string                          `json:"name"`
	Description *string                          `json:"description"`
	Enabled     *bool                            `json:"enabled"`
	Repeat      *bool                            `json:"repeat"`
	Steps       *[]*storage.EscalationPolicyStep `json:"steps"`
}

// Apply preserves omitted fields from the current transactional snapshot. Labels
// hidden from a form must never be interpreted as an instruction to clear IDs.
func (in EscalationPolicyInput) Apply(current *storage.EscalationPolicy, canReadChannels bool) (*storage.EscalationPolicy, error) {
	if current == nil && !canReadChannels {
		return nil, ErrEscalationChannelPermission
	}
	next := storage.EscalationPolicy{}
	if current != nil {
		next = *current
	}
	if in.Name != nil {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		next.Description = strings.TrimSpace(*in.Description)
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.Repeat != nil {
		next.Repeat = *in.Repeat
	}
	if in.Steps != nil {
		if current != nil && !canReadChannels && !storage.SameEscalationSteps(current.Steps, *in.Steps) {
			return nil, ErrEscalationChannelPermission
		}
		next.Steps = *in.Steps
	}
	if err := validate.ValidateEscalationPolicy(&next); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEscalationPolicy, err)
	}
	return &next, nil
}
