package api

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

// MaxConfigurationBytes bounds both JSON and multipart imports. The normal
// small request limit does not apply to these authenticated transfer routes.
const MaxConfigurationBytes int64 = 32 << 20

func validateImportedConfiguration(c *storage.ConfigurationSnapshot) error {
	if len(c.Monitors)+len(c.Groups)+len(c.Tags)+len(c.Proxies)+len(c.Channels)+len(c.EscalationPolicies)+len(c.Rotations)+len(c.Maintenance)+len(c.StatusPages)+len(c.Agents) > 100000 {
		return fmt.Errorf("configuration exceeds 100000 entities")
	}
	for _, m := range c.Monitors {
		if m == nil {
			return fmt.Errorf("monitor must not be null")
		}
		if err := validate.ValidateMonitor(m); err != nil {
			return fmt.Errorf("monitor %d: %w", m.ID, err)
		}
	}
	for _, g := range c.Groups {
		if g == nil {
			return fmt.Errorf("group must not be null")
		}
		if err := validate.ValidateMonitorGroup(g); err != nil {
			return fmt.Errorf("group %d: %w", g.ID, err)
		}
	}
	for _, tag := range c.Tags {
		if tag == nil {
			return fmt.Errorf("tag must not be null")
		}
		if err := validate.ValidateTag(tag); err != nil {
			return fmt.Errorf("tag %d: %w", tag.ID, err)
		}
	}
	for _, proxy := range c.Proxies {
		if proxy == nil {
			return fmt.Errorf("proxy must not be null")
		}
		if err := validate.ValidateProxy(proxy); err != nil {
			return fmt.Errorf("proxy %d: %w", proxy.ID, err)
		}
		if len(proxy.AuthUser) > 4096 || len(proxy.AuthPass) > 16384 || strings.HasPrefix(proxy.AuthPass, "enc:") {
			return fmt.Errorf("proxy %d credentials must be plaintext and within size limits", proxy.ID)
		}
	}
	for _, ch := range c.Channels {
		if ch == nil {
			return fmt.Errorf("notification channel must not be null")
		}
		if err := validate.ValidateNotificationChannel(ch); err != nil {
			return fmt.Errorf("channel %d: invalid name, type, settings or events", ch.ID)
		}
	}
	for _, policy := range c.EscalationPolicies {
		if policy == nil || len(policy.Steps) > 1000 {
			return fmt.Errorf("escalation policy must have at most 1000 steps")
		}
		for _, step := range policy.Steps {
			if step == nil || step.DelayMinutes < 0 || int64(step.DelayMinutes) > math.MaxInt64/int64(time.Minute) {
				return fmt.Errorf("policy %d has an invalid step delay", policy.ID)
			}
		}
		if err := validate.ValidateEscalationPolicy(policy); err != nil {
			return fmt.Errorf("policy %d: %w", policy.ID, err)
		}
	}
	for _, r := range c.Rotations {
		if r == nil || len(r.Name) > 255 || r.PeriodStartedAt.IsZero() || r.PeriodStartedAt.Year() < 1 || r.PeriodStartedAt.Year() > 9999 {
			return fmt.Errorf("rotation requires a valid name and period_started_at")
		}
	}
	for _, mw := range c.Maintenance {
		if mw == nil || len(mw.Name) > 255 || len(mw.CronExpr) > 255 {
			return fmt.Errorf("maintenance name or cron expression exceeds limits")
		}
		if err := validate.ValidateMaintenanceWindow(mw); err != nil {
			return fmt.Errorf("maintenance %d: %w", mw.ID, err)
		}
	}
	for _, p := range c.StatusPages {
		if p == nil {
			return fmt.Errorf("status page must not be null")
		}
		original := *p
		if err := validate.ValidateStatusPage(p); err != nil {
			return fmt.Errorf("status page %d: %w", p.ID, err)
		}
		// Imported protection/branding must not silently change through slug
		// substitution or markup sanitization. Existing safe exports are stable.
		if p.Slug != original.Slug || p.CustomCSS != original.CustomCSS || p.CustomHeaderHTML != original.CustomHeaderHTML || p.AnalyticsScript != original.AnalyticsScript {
			return fmt.Errorf("status page %d contains an invalid slug or unsafe/noncanonical markup; save it through the status page editor first", p.ID)
		}
		if p.PasswordHash != "" {
			decoded, err := hex.DecodeString(p.PasswordHash)
			if err != nil || len(decoded) != 32 || strings.ToLower(p.PasswordHash) != p.PasswordHash {
				return fmt.Errorf("status page %d password_hash must be a lowercase SHA-256 digest", p.ID)
			}
		}
	}
	for _, relation := range c.StatusPageMonitors {
		if len(relation.GroupName) > 255 {
			return fmt.Errorf("status page monitor group name exceeds 255 characters")
		}
	}
	for _, agent := range c.Agents {
		if strings.TrimSpace(agent.Name) == "" || len(agent.Name) > 255 {
			return fmt.Errorf("agent declaration requires a name of at most 255 characters")
		}
	}
	return nil
}
