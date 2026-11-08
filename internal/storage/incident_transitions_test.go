package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestIncidentTransitionsRollbackOnTimelineFailure(t *testing.T) {
	for _, action := range []string{"create", "failure", "acknowledge", "resolve", "severity", "recovery"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			mon := newTestMonitor(t, s, "atomic incident")
			var before *Incident
			if action != "create" {
				var err error
				before, _, err = s.RecordIncidentFailure(ctx, mon.ID, "warning", "slow")
				if err != nil {
					t.Fatal(err)
				}
				policy := &EscalationPolicy{Name: "policy", Enabled: true}
				if err := s.CreateEscalationPolicy(ctx, policy); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateEscalationState(ctx, &EscalationState{IncidentID: before.ID, PolicyID: policy.ID, NextFireAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.writeDB.Exec(`CREATE TRIGGER reject_incident_event BEFORE INSERT ON incident_events BEGIN SELECT RAISE(ABORT, 'injected timeline failure'); END`); err != nil {
				t.Fatal(err)
			}
			var got *Incident
			var err error
			switch action {
			case "create", "failure":
				got, _, err = s.RecordIncidentFailure(ctx, mon.ID, "critical", "down")
			case "recovery":
				got, _, err = s.RecordIncidentRecovery(ctx, mon.ID)
			default:
				got, err = s.TransitionIncident(ctx, before.ID, IncidentTransition{Action: action, Actor: "operator", Severity: "critical"}, MonitorAccess{All: true})
			}
			if err == nil || got != nil {
				t.Fatalf("failed timeline returned successful mutation: %+v, %v", got, err)
			}
			if before == nil {
				if _, err := s.GetOpenIncident(ctx, mon.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("failed create left incident: %v", err)
				}
				return
			}
			stored, err := s.GetIncident(ctx, before.ID)
			if err != nil || !reflect.DeepEqual(before, stored) {
				t.Fatalf("failure partially changed incident: before %+v after %+v, %v", before, stored, err)
			}
			if _, err := s.GetEscalationStateByIncident(ctx, before.ID); err != nil {
				t.Fatalf("failure cancelled escalation: %v", err)
			}
			events, err := s.ListIncidentEvents(ctx, before.ID)
			if err != nil || len(events) != 1 || events[0].Type != "created" {
				t.Fatalf("failed mutation altered timeline: %+v, %v", events, err)
			}
		})
	}
}

func TestIncidentTransitionsUseCurrentScopeAndExactColumns(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	mon := monitors[0]
	inc, _, err := s.RecordIncidentFailure(ctx, mon.ID, "warning", "original cause")
	if err != nil {
		t.Fatal(err)
	}
	access := MonitorAccess{GroupIDs: []int64{groups[0].ID}}
	for _, denied := range []MonitorAccess{{}, {GroupIDs: []int64{groups[1].ID}}} {
		for _, action := range []string{"acknowledge", "resolve", "severity"} {
			if _, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: action, Actor: "denied", Severity: "critical"}, denied); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("denied %s returned %v", action, err)
			}
		}
	}
	ack, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "acknowledge", Actor: "alice"}, access)
	if err != nil || ack.Status != "acknowledged" || ack.EscalationPending || !ack.AnnouncementPending {
		t.Fatalf("ack: %+v, %v", ack, err)
	}
	if _, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "acknowledge", Actor: "bob"}, access); !errors.Is(err, ErrIncidentConflict) {
		t.Fatalf("second acknowledgement was accepted: %v", err)
	}
	raised, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "severity", Actor: "bob", Severity: "critical"}, access)
	if err != nil || raised.AcknowledgedBy != "alice" || raised.Cause != "original cause" || raised.Status != "acknowledged" {
		t.Fatalf("severity overwrote another action: %+v, %v", raised, err)
	}
	if _, err := s.BulkSetMonitorGroup(ctx, []int64{mon.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "resolve", Actor: "alice"}, access); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("former group still authorizes resolution: %v", err)
	}
	resolved, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "resolve", Actor: "owner"}, MonitorAccess{MonitorIDs: []int64{mon.ID}})
	if err != nil || resolved.Status != "resolved" || resolved.AnnouncementPending || resolved.EscalationPending || resolved.AcknowledgedBy != "alice" {
		t.Fatalf("resolve: %+v, %v", resolved, err)
	}
}

func TestIncidentManualResolveCannotBeRevivedByFailure(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		s := testStore(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mon := newTestMonitor(t, s, "concurrent incident")
		inc, _, err := s.RecordIncidentFailure(ctx, mon.ID, "warning", "slow")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errorsOut := make(chan error, 2)
		go func() {
			<-start
			_, err := s.TransitionIncident(ctx, inc.ID, IncidentTransition{Action: "resolve", Actor: "operator"}, MonitorAccess{All: true})
			if recovery && errors.Is(err, ErrIncidentConflict) {
				err = nil // automatic recovery may win the single valid transition
			}
			errorsOut <- err
		}()
		go func() {
			<-start
			var err error
			if recovery {
				_, _, err = s.RecordIncidentRecovery(ctx, mon.ID)
			} else {
				_, _, err = s.RecordIncidentFailure(ctx, mon.ID, "critical", "hard failure")
			}
			errorsOut <- err
		}()
		close(start)
		for range 2 {
			if err := <-errorsOut; err != nil {
				t.Fatal(err)
			}
		}
		stored, err := s.GetIncident(ctx, inc.ID)
		if err != nil || stored.Status != "resolved" || stored.ResolvedAt == nil {
			t.Fatalf("concurrent transition revived old incident: %+v, %v", stored, err)
		}
		events, err := s.ListIncidentEvents(ctx, inc.ID)
		if err != nil {
			t.Fatal(err)
		}
		resolutions := 0
		for _, event := range events {
			if event.Type == "resolved" || event.Type == "check_recovered" {
				resolutions++
			}
		}
		if resolutions != 1 {
			t.Fatalf("expected one committed resolution, got %d", resolutions)
		}
		// A later real failure must always use a fresh ID, irrespective of which
		// concurrent operation won the writer transaction above.
		next, _, err := s.RecordIncidentFailure(ctx, mon.ID, "critical", "still down")
		if err != nil || next.ID == inc.ID || next.Status != "open" {
			t.Fatalf("new failure reused resolved row: %+v, %v", next, err)
		}
	}
}

func TestIncidentRecoveryRetainsOnlyEventAnnouncementDecision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mon := newTestMonitor(t, s, "silent incident")
	inc, _, err := s.RecordIncidentFailure(ctx, mon.ID, "critical", "maintenance outage")
	if err != nil {
		t.Fatal(err)
	}
	event, resolved, err := s.RecordIncidentRecovery(ctx, mon.ID)
	if err != nil || !resolved || !event.AnnouncementPending || event.Status != "resolved" {
		t.Fatalf("recovery lost the prior suppression decision: %+v, %v", event, err)
	}
	stored, err := s.GetIncident(ctx, inc.ID)
	if err != nil || stored.Status != "resolved" || stored.AnnouncementPending || stored.EscalationPending {
		t.Fatalf("resolved row kept obsolete pending state: %+v, %v", stored, err)
	}
}
