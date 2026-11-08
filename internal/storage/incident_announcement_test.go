package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIncidentAnnouncementMigrationPreservesLegacyState(t *testing.T) {
	var id int64
	s := reopenAtVersion(t, 36, func(s *SQLiteStore) {
		mon := newTestMonitor(t, s, "legacy incident")
		result, err := s.writeDB.Exec("INSERT INTO incidents (monitor_id, status, cause) VALUES (?, 'open', 'legacy')", mon.ID)
		if err != nil {
			t.Fatal(err)
		}
		id, err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	})
	inc, err := s.GetIncident(context.Background(), id)
	if err != nil || inc.Cause != "legacy" || inc.AnnouncementPending || inc.EscalationPending {
		t.Fatalf("upgrade changed legacy announcement state: %+v %v", inc, err)
	}
}

func TestIncidentAnnouncementStateIsInternalAndPreservedByEdits(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mon := newTestMonitor(t, s, "announcement")
	inc := &Incident{MonitorID: mon.ID, Status: "open", AnnouncementPending: true, EscalationPending: true}
	if err := s.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	// Public edits cannot clear internal delivery state by omitting it.
	inc.AnnouncementPending = false
	inc.Cause = "edited"
	if err := s.UpdateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetIncident(ctx, inc.ID)
	if err != nil || !stored.AnnouncementPending || !stored.EscalationPending {
		t.Fatalf("edit erased pending announcement: %+v %v", stored, err)
	}
	b, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(b), `"announcement_pending"`) || strings.Contains(string(b), `"AnnouncementPending"`) || strings.Contains(string(b), `"EscalationPending"`) {
		t.Fatalf("public JSON exposed internal delivery state: %s %v", b, err)
	}
	if err := s.MarkIncidentAnnounced(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetIncident(ctx, inc.ID)
	if err != nil || stored.AnnouncementPending {
		t.Fatalf("acknowledgement did not persist: %+v %v", stored, err)
	}
}

func TestIncidentEscalationSetupIsAtomicAndDoesNotRestartCompletedChain(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mon := newTestMonitor(t, s, "escalation")
	policy := &EscalationPolicy{Name: "policy", Enabled: true}
	if err := s.CreateEscalationPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	inc := &Incident{MonitorID: mon.ID, Status: "open", EscalationPending: true}
	if err := s.CreateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	state := &EscalationState{IncidentID: inc.ID, PolicyID: policy.ID, NextFireAt: time.Now()}
	if _, err := s.writeDB.Exec(`CREATE TRIGGER reject_escalation_ack BEFORE UPDATE OF escalation_pending ON incidents
		BEGIN SELECT RAISE(ABORT, 'injected acknowledgement failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareIncidentEscalation(ctx, inc.ID, state); err == nil {
		t.Fatal("injected acknowledgement failure was ignored")
	}
	if pending, err := s.ListPendingEscalationStates(ctx, time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatalf("runner can see unacknowledged escalation state: %+v %v", pending, err)
	}
	stored, err := s.GetIncident(ctx, inc.ID)
	if err != nil || !stored.EscalationPending {
		t.Fatalf("failed setup erased retry state: %+v %v", stored, err)
	}
	if _, err := s.writeDB.Exec("DROP TRIGGER reject_escalation_ack"); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareIncidentEscalation(ctx, inc.ID, state); err != nil {
		t.Fatal(err)
	}
	active, err := s.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatal(err)
	}
	active.CurrentStep = 1
	if err := s.UpdateEscalationState(ctx, active); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareIncidentEscalation(ctx, inc.ID, state); err != nil {
		t.Fatal(err)
	}
	active, err = s.GetEscalationStateByIncident(ctx, inc.ID)
	if err != nil || active.CurrentStep != 1 {
		t.Fatalf("retry reset active escalation: %+v %v", active, err)
	}
	// Completion deletes the runner's state; acknowledgement must still prevent
	// a retry from creating it again using an earlier incident snapshot.
	if err := s.DeleteEscalationStateByIncident(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareIncidentEscalation(ctx, inc.ID, state); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.ListPendingEscalationStates(ctx, time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatalf("retry restarted completed escalation: %+v %v", pending, err)
	}
}
