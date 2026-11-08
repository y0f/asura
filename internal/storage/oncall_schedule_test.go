package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func rotationFixture(t *testing.T, s *SQLiteStore) *OnCallRotation {
	t.Helper()
	r := &OnCallRotation{Name: "primary", Period: "daily"}
	for i := range 3 {
		ch := &NotificationChannel{Name: fmt.Sprintf("channel-%d", i), Type: "webhook", Settings: []byte("{}"), Enabled: true}
		if err := s.CreateNotificationChannel(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
		r.ChannelIDs = append(r.ChannelIDs, ch.ID)
	}
	if err := s.CreateOnCallRotation(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRotationPeriodBoundariesAndCatchup(t *testing.T) {
	anchor := time.Date(2026, 10, 24, 8, 30, 0, 0, time.UTC)
	for _, period := range []string{"daily", "weekly"} {
		duration := 24 * time.Hour
		if period == "weekly" {
			duration *= 7
		}
		for _, tc := range []struct {
			elapsed time.Duration
			index   int
			steps   int
		}{
			{-time.Hour, 1, 0}, {time.Hour, 1, 0}, {duration - time.Nanosecond, 1, 0},
			{duration, 2, 1}, {duration*5 + time.Hour, 0, 5}, {duration * 3000, 1, 3000},
		} {
			t.Run(fmt.Sprintf("%s/%s", period, tc.elapsed), func(t *testing.T) {
				r := OnCallRotation{ChannelIDs: []int64{10, 20, 30}, CurrentIndex: 1, Period: period, PeriodStartedAt: anchor}
				projectRotation(&r, anchor.Add(tc.elapsed).In(time.FixedZone("offset", 3600)))
				if r.CurrentIndex != tc.index || !r.PeriodStartedAt.Equal(anchor.Add(time.Duration(tc.steps)*duration)) {
					t.Fatalf("wrong period/index: %+v", r)
				}
			})
		}
	}
}

func TestRotationReadsAndRepeatedAdvancePreserveCadence(t *testing.T) {
	s := testStore(t)
	r := rotationFixture(t, s)
	ctx := context.Background()
	anchor := time.Now().UTC().Truncate(time.Second).Add(-5*24*time.Hour - 2*time.Hour)
	if _, err := s.writeDB.Exec(`UPDATE on_call_rotations SET current_index=1, period_started_at=? WHERE id=?`, formatTime(anchor), r.ID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, err := s.GetOnCallRotation(ctx, r.ID)
		if err != nil || got.CurrentIndex != 0 || !got.PeriodStartedAt.Equal(anchor.Add(5*24*time.Hour)) {
			t.Fatalf("late tick/repeated read changed cadence: %+v %v", got, err)
		}
		if got.CurrentOnCallChannelID() != r.ChannelIDs[0] {
			t.Fatal("effective contact differs from projected index")
		}
		if err := s.AdvanceRotation(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	var storedAnchor, updatedAt string
	if err := s.readDB.QueryRow(`SELECT period_started_at, updated_at FROM on_call_rotations WHERE id=?`, r.ID).Scan(&storedAnchor, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if storedAnchor != formatTime(anchor.Add(5*24*time.Hour)) || updatedAt != formatTime(r.UpdatedAt) {
		t.Fatalf("advancer rewrote audit time or introduced cadence drift: %s %s", storedAnchor, updatedAt)
	}
}

func TestRotationEditsAndOverridesKeepCurrentCadence(t *testing.T) {
	s := testStore(t)
	r := rotationFixture(t, s)
	ctx := context.Background()
	anchor := time.Now().UTC().Truncate(time.Second).Add(-25 * time.Hour)
	if _, err := s.writeDB.Exec(`UPDATE on_call_rotations SET period_started_at=? WHERE id=?`, formatTime(anchor), r.ID); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	got, err := s.UpdateOnCallRotation(ctx, r.ID, func(current *OnCallRotation) error {
		current.Name = "renamed"
		current.OverrideChannelID, current.OverrideUntil = &r.ChannelIDs[2], &expiry
		return nil
	})
	if err != nil || got.CurrentIndex != 1 || !got.PeriodStartedAt.Equal(anchor.Add(24*time.Hour)) || got.CurrentOnCallChannelID() != r.ChannelIDs[2] {
		t.Fatalf("override/rename changed base schedule: %+v %v", got, err)
	}
	if got.OverrideActive(expiry) || !got.OverrideActive(expiry.Add(-time.Nanosecond)) {
		t.Fatal("override expiry boundary is not exclusive")
	}
	got, err = s.UpdateOnCallRotation(ctx, r.ID, func(current *OnCallRotation) error {
		current.OverrideChannelID, current.OverrideUntil = nil, nil
		return nil
	})
	if err != nil || got.CurrentOnCallChannelID() != r.ChannelIDs[1] || !got.PeriodStartedAt.Equal(anchor.Add(24*time.Hour)) {
		t.Fatalf("clearing override reset cadence: %+v %v", got, err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	got, err = s.UpdateOnCallRotation(ctx, r.ID, func(current *OnCallRotation) error {
		current.Period, current.CurrentIndex = "weekly", 2
		return nil
	})
	if err != nil || got.PeriodStartedAt.Before(before) || got.CurrentIndex != 2 {
		t.Fatalf("schedule edit did not start a new period: %+v %v", got, err)
	}
	for _, edit := range []func(*OnCallRotation) error{
		func(r *OnCallRotation) error { r.CurrentIndex = -1; return nil },
		func(r *OnCallRotation) error { r.ChannelIDs = []int64{99999}; r.CurrentIndex = 0; return nil },
		func(r *OnCallRotation) error { r.Period = "hourly"; return nil },
		func(r *OnCallRotation) error { r.Name = "must roll back"; return errors.New("callback rejected") },
	} {
		if _, err := s.UpdateOnCallRotation(ctx, r.ID, edit); err == nil {
			t.Fatal("invalid edit accepted")
		}
		unchanged, err := s.GetOnCallRotation(ctx, r.ID)
		if err != nil || unchanged.Name != got.Name || unchanged.CurrentIndex != got.CurrentIndex || unchanged.Period != got.Period || !unchanged.PeriodStartedAt.Equal(got.PeriodStartedAt) {
			t.Fatalf("rejected edit partially committed: %+v %v", unchanged, err)
		}
	}
}

func TestRotationLegacyIndexCannotPanic(t *testing.T) {
	r := OnCallRotation{ChannelIDs: []int64{10, 20, 30}, CurrentIndex: -100, Period: "daily"}
	if got := r.CurrentOnCallChannelID(); got != 30 {
		t.Fatalf("invalid legacy index not normalized: %d", got)
	}
	if r.CurrentIndex != -100 {
		t.Fatal("contact lookup mutated shared snapshot")
	}
}

func TestUpgradeFromV37PreservesRotationAnchor(t *testing.T) {
	var id int64
	anchor := time.Now().UTC().Truncate(time.Second).Add(-49 * time.Hour)
	s := reopenAtVersion(t, 37, func(s *SQLiteStore) {
		result, err := s.writeDB.Exec(`INSERT INTO on_call_rotations (name, channel_ids, period, current_index, updated_at)
		 VALUES ('legacy', '[10,20,30]', 'daily', 1, ?)`, formatTime(anchor))
		if err != nil {
			t.Fatal(err)
		}
		id, err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	})
	r, err := s.GetOnCallRotation(context.Background(), id)
	if err != nil || r.CurrentIndex != 0 || !r.PeriodStartedAt.Equal(anchor.Add(48*time.Hour)) {
		t.Fatalf("upgrade/reopen lost current holder or cadence: %+v %v", r, err)
	}
}
