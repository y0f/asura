package web

import (
	"net/url"
	"slices"
	"testing"
)

func TestNotificationEventSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		form    url.Values
		want    []string
		invalid bool
	}{
		{"escalation filter survives edit", url.Values{"event_mode": {"selected"}, "event_types": {"incident.escalated"}}, []string{"incident.escalated"}, false},
		{"SLA and incident filters survive edit", url.Values{"event_mode": {"selected"}, "event_types": {"sla.breach", "incident.created"}}, []string{"sla.breach", "incident.created"}, false},
		{"explicit all ignores disabled selections", url.Values{"event_mode": {"all"}, "event_types": {"incident.created"}}, nil, false},
		{"empty selection never broadens to all", url.Values{"event_mode": {"selected"}}, nil, true},
		{"unknown selection reaches shared validation", url.Values{"event_mode": {"selected"}, "event_types": {"future.event"}}, []string{"future.event"}, false},
		{"invalid selection mode rejected", url.Values{"event_mode": {"future"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := buildFormRequest(tc.form)
			got := parseNotificationEvents(r)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("events = %v, want %v", got, tc.want)
			}
			if err := notificationEventSelectionError(r, got); (err != nil) != tc.invalid {
				t.Fatalf("selection error = %v, want invalid %v", err, tc.invalid)
			}
		})
	}
}
