package checker

import "time"

// Displayed days are rounded down; expiry itself is an exact instant.
func expiryStatus(expiry, now time.Time, warnDays int) (string, int) {
	days := int(expiry.Sub(now).Hours() / 24)
	if !expiry.After(now) {
		return "down", days
	}
	if days <= warnDays {
		return "degraded", days
	}
	return "up", days
}
