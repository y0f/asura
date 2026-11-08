package httputil

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
)

var ErrInvalidProxy = errors.New("invalid proxy")

// ProxyInput preserves omitted fields against the current transactional row.
// Read-only fields are accepted for a redacted GET/edit/PUT round trip.
type ProxyInput struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Name          *string   `json:"name"`
	Protocol      *string   `json:"protocol"`
	Host          *string   `json:"host"`
	Port          *int      `json:"port"`
	AuthUser      *string   `json:"auth_user"`
	AuthPass      *string   `json:"auth_pass"`
	Enabled       *bool     `json:"enabled"`
	ClearAuthPass bool      `json:"clear_auth_pass"`
}

func (in ProxyInput) Apply(current *storage.Proxy, canRetargetSecrets bool) (*storage.Proxy, error) {
	next := storage.Proxy{}
	if current != nil {
		next = *current
	}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Protocol != nil {
		next.Protocol = *in.Protocol
	}
	if in.Host != nil {
		next.Host = *in.Host
	}
	if in.Port != nil {
		next.Port = *in.Port
	}
	if in.AuthUser != nil {
		next.AuthUser = *in.AuthUser
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	retainsPassword := current != nil && current.AuthPass != ""
	if in.ClearAuthPass {
		next.AuthPass = ""
		retainsPassword = false
	} else if in.AuthPass != nil && *in.AuthPass != "" {
		next.AuthPass = *in.AuthPass
		retainsPassword = false
	}
	if retainsPassword && !canRetargetSecrets && (next.Protocol != current.Protocol || !strings.EqualFold(next.Host, current.Host) || next.Port != current.Port || next.AuthUser != current.AuthUser) {
		return nil, fmt.Errorf("%w: changing a credential destination requires replacing or removing its saved password", ErrInvalidProxy)
	}
	if err := validate.ValidateProxy(&next); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidProxy, err)
	}
	return &next, nil
}
