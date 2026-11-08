package httputil

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/y0f/asura/v2/internal/redact"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
)

var ErrInvalidNotification = errors.New("invalid notification channel")

type NotificationInput struct {
	ID           int64           `json:"id"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Name         *string         `json:"name"`
	Type         *string         `json:"type"`
	Enabled      *bool           `json:"enabled"`
	Settings     json.RawMessage `json:"settings"`
	Events       json.RawMessage `json:"events"`
	ClearSecrets []string        `json:"clear_secrets"`
}

// Apply uses the current transactional row, so omitted metadata, events and
// secrets survive concurrent edits. A type change never inherits credentials.
func (in NotificationInput) Apply(current *storage.NotificationChannel, canRetarget bool) (*storage.NotificationChannel, error) {
	next := storage.NotificationChannel{Enabled: true}
	if current != nil {
		next = *current
		next.Settings = slices.Clone(current.Settings)
		next.Events = slices.Clone(current.Events)
	}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Type != nil {
		next.Type = *in.Type
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if len(in.Events) != 0 {
		if err := json.Unmarshal(in.Events, &next.Events); err != nil {
			return nil, fmt.Errorf("%w: events must be an array of event names or null", ErrInvalidNotification)
		}
	}
	next.Settings = slices.Clone(in.Settings)
	if current != nil && next.Type == current.Type {
		clear := make(map[string]bool, len(in.ClearSecrets))
		for _, key := range in.ClearSecrets {
			clear[key] = true
		}
		var err error
		next.Settings, err = redact.MergeNotificationSecrets(next.Type, next.Settings, current.Settings, clear, canRetarget)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidNotification, err)
		}
	}
	if err := validate.ValidateNotificationChannel(&next); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNotification, err)
	}
	return &next, nil
}
