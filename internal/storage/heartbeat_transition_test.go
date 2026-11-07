package storage

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestHeartbeatPingUpdatesVisibleStatusAtomically(t *testing.T) {
	s, ctx, mon := createTestHeartbeat(t)
	if err := s.UpdateHeartbeatPing(ctx, mon.ID, "abc123def456"); err != nil {
		t.Fatal(err)
	}
	hb, err := s.GetHeartbeatByMonitorID(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.GetMonitorStatus(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hb.Status != "up" || status.Status != "up" || hb.LastPingAt == nil || status.LastCheckAt == nil ||
		!hb.LastPingAt.Equal(*status.LastCheckAt) || status.ConsecSuccesses != mon.SuccessThreshold {
		t.Fatalf("first ping left inconsistent state: heartbeat=%+v monitor=%+v", hb, status)
	}
	for _, token := range []string{"unknown", hb.Token} {
		if err := s.UpdateHeartbeatPing(ctx, mon.ID, token); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("invalid token accepted: %v", err)
		}
	}
	if err := s.UpdateHeartbeatPing(ctx, mon.ID+1, "abc123def456"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("token accepted for another monitor: %v", err)
	}
}

func TestHeartbeatExpiryRechecksDeadline(t *testing.T) {
	s, ctx, mon := createTestHeartbeat(t)
	if _, err := s.writeDB.ExecContext(ctx, `UPDATE heartbeats SET status='up', last_ping_at=? WHERE monitor_id=?`,
		formatTime(time.Now().Add(-time.Hour)), mon.ID); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ListExpiredHeartbeats(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expected expired candidate: %v %v", candidates, err)
	}
	if err := s.UpdateHeartbeatPing(ctx, mon.ID, "abc123def456"); err != nil {
		t.Fatal(err)
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || won {
		t.Fatalf("expiry overwrote newer ping: won=%v err=%v", won, err)
	}
	// Repeated pings within the same timestamp second must also remain healthy.
	if err := s.UpdateHeartbeatPing(ctx, mon.ID, "abc123def456"); err != nil {
		t.Fatal(err)
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || won {
		t.Fatalf("same-second ping was expired: won=%v err=%v", won, err)
	}
	if _, err := s.writeDB.ExecContext(ctx, `UPDATE heartbeats SET last_ping_at=? WHERE monitor_id=?`,
		formatTime(time.Now().Add(-time.Hour)), mon.ID); err != nil {
		t.Fatal(err)
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || !won {
		t.Fatalf("missed real expiry: won=%v err=%v", won, err)
	}
	status, err := s.GetMonitorStatus(ctx, mon.ID)
	if err != nil || status.Status != "down" || status.ConsecFails != mon.FailureThreshold {
		t.Fatalf("expiry did not update visible state: %+v %v", status, err)
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || won {
		t.Fatalf("repeated expiry won twice: won=%v err=%v", won, err)
	}
}

func TestHeartbeatTransitionsRollbackOnStatusFailure(t *testing.T) {
	s, ctx, mon := createTestHeartbeat(t)
	if _, err := s.writeDB.ExecContext(ctx, `UPDATE heartbeats SET status='up', last_ping_at=? WHERE monitor_id=?`,
		formatTime(time.Now().Add(-time.Hour)), mon.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetHeartbeatByMonitorID(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.writeDB.ExecContext(ctx, `CREATE TRIGGER reject_heartbeat_status BEFORE INSERT ON monitor_status
		BEGIN SELECT RAISE(ABORT, 'injected status failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHeartbeatPing(ctx, mon.ID, "abc123def456"); err == nil {
		t.Fatal("ping ignored status write failure")
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err == nil || won {
		t.Fatalf("expiry ignored status write failure: won=%v err=%v", won, err)
	}
	after, err := s.GetHeartbeatByMonitorID(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || !after.LastPingAt.Equal(*before.LastPingAt) {
		t.Fatalf("failed transition partly committed: before=%+v after=%+v", before, after)
	}
}

func TestHeartbeatExpirySkipsPendingAndDisabled(t *testing.T) {
	s, ctx, mon := createTestHeartbeat(t)
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || won {
		t.Fatalf("heartbeat without first ping expired: won=%v err=%v", won, err)
	}
	if _, err := s.writeDB.ExecContext(ctx, `UPDATE heartbeats SET last_ping_at=? WHERE monitor_id=?`,
		formatTime(time.Now().Add(-time.Hour)), mon.ID); err != nil {
		t.Fatal(err)
	}
	mon.Enabled = false
	if err := s.UpdateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	if won, err := s.ExpireHeartbeat(ctx, mon.ID); err != nil || won {
		t.Fatalf("disabled monitor expired: won=%v err=%v", won, err)
	}
}
