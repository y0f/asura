package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateOnCallRotation(ctx context.Context, r *OnCallRotation) error {
	if err := validateRotation(r); err != nil {
		return err
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkRotationChannels(ctx, tx, r); err != nil {
		return err
	}
	channelIDs, _ := json.Marshal(r.ChannelIDs)
	now := formatTime(time.Now())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO on_call_rotations (name, channel_ids, period, current_index, period_started_at,
		 override_channel_id, override_until, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, string(channelIDs), r.Period, r.CurrentIndex, now, r.OverrideChannelID, rotationOverrideUntil(r), now, now)
	if err != nil {
		return fmt.Errorf("create on-call rotation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.ID = id
	r.CreatedAt = parseTime(now)
	r.UpdatedAt = parseTime(now)
	r.PeriodStartedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetOnCallRotation(ctx context.Context, id int64) (*OnCallRotation, error) {
	return scanRotation(s.readDB.QueryRowContext(ctx,
		`SELECT `+rotationColumns+` FROM on_call_rotations WHERE id=?`, id), time.Now())
}

const rotationColumns = `id, name, channel_ids, period, current_index, period_started_at,
	override_channel_id, override_until, created_at, updated_at`

func scanRotation(row scanner, now time.Time) (*OnCallRotation, error) {
	var r OnCallRotation
	var channelIDs, startedAt, createdAt, updatedAt string
	var overrideID sql.NullInt64
	var overrideUntil sql.NullString
	err := row.Scan(&r.ID, &r.Name, &channelIDs, &r.Period, &r.CurrentIndex, &startedAt, &overrideID, &overrideUntil, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(channelIDs), &r.ChannelIDs); err != nil {
		return nil, fmt.Errorf("decode rotation channels: %w", err)
	}
	if overrideID.Valid {
		oid := overrideID.Int64
		r.OverrideChannelID = &oid
	}
	r.OverrideUntil = parseTimePtr(overrideUntil)
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	r.PeriodStartedAt = parseTime(startedAt)
	projectRotation(&r, now)
	return &r, nil
}

func (s *SQLiteStore) ListOnCallRotations(ctx context.Context) ([]*OnCallRotation, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT `+rotationColumns+` FROM on_call_rotations ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rotations []*OnCallRotation
	now := time.Now()
	for rows.Next() {
		r, err := scanRotation(rows, now)
		if err != nil {
			return nil, err
		}
		rotations = append(rotations, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if rotations == nil {
		rotations = []*OnCallRotation{}
	}
	return rotations, nil
}

// UpdateOnCallRotation edits the current schedule atomically. edit must not do
// I/O: it runs while the storage writer is held. Name/override changes preserve
// cadence; membership, period or index changes start a new complete period.
func (s *SQLiteStore) UpdateOnCallRotation(ctx context.Context, id int64, edit func(*OnCallRotation) error) (*OnCallRotation, error) {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Truncate(time.Second)
	r, err := scanRotation(tx.QueryRowContext(ctx, `SELECT `+rotationColumns+` FROM on_call_rotations WHERE id=?`, id), now)
	if err != nil {
		return nil, err
	}
	index, period, anchor, createdAt := r.CurrentIndex, r.Period, r.PeriodStartedAt, r.CreatedAt
	channels := slices.Clone(r.ChannelIDs)
	if err := edit(r); err != nil {
		return nil, err
	}
	if err := validateRotation(r); err != nil {
		return nil, err
	}
	if err := checkRotationChannels(ctx, tx, r); err != nil {
		return nil, err
	}
	if r.CurrentIndex != index || r.Period != period || !slices.Equal(channels, r.ChannelIDs) {
		anchor = now
	}
	r.ID, r.CreatedAt, r.UpdatedAt, r.PeriodStartedAt = id, createdAt, now, anchor
	channelIDs, _ := json.Marshal(r.ChannelIDs)
	_, err = tx.ExecContext(ctx,
		`UPDATE on_call_rotations SET name=?, channel_ids=?, period=?, current_index=?, period_started_at=?,
		 override_channel_id=?, override_until=?, updated_at=? WHERE id=?`,
		r.Name, string(channelIDs), r.Period, r.CurrentIndex, formatTime(anchor),
		r.OverrideChannelID, rotationOverrideUntil(r), formatTime(now), id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *SQLiteStore) DeleteOnCallRotation(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM on_call_rotations WHERE id=?", id)
	return err
}

func (s *SQLiteStore) AdvanceRotation(ctx context.Context, id int64) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := scanRotation(tx.QueryRowContext(ctx, `SELECT `+rotationColumns+` FROM on_call_rotations WHERE id=?`, id), time.Now())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE on_call_rotations SET current_index=?, period_started_at=?
		WHERE id=? AND (current_index!=? OR period_started_at!=?)`,
		r.CurrentIndex, formatTime(r.PeriodStartedAt), id, r.CurrentIndex, formatTime(r.PeriodStartedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// CurrentOnCallChannelID returns the channel ID that is currently on call.
func (r *OnCallRotation) CurrentOnCallChannelID() int64 {
	now := time.Now().UTC()
	if r.OverrideActive(now) {
		return *r.OverrideChannelID
	}
	if len(r.ChannelIDs) == 0 {
		return 0
	}
	copy := *r
	projectRotation(&copy, now)
	return copy.ChannelIDs[copy.CurrentIndex]
}

func (r *OnCallRotation) OverrideActive(now time.Time) bool {
	return r.OverrideChannelID != nil && *r.OverrideChannelID > 0 && r.OverrideUntil != nil && now.Before(*r.OverrideUntil)
}

var ErrInvalidRotation = errors.New("invalid on-call rotation")

func validateRotation(r *OnCallRotation) error {
	switch {
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("%w: name is required", ErrInvalidRotation)
	case r.Period != "daily" && r.Period != "weekly":
		return fmt.Errorf("%w: period must be daily or weekly", ErrInvalidRotation)
	case len(r.ChannelIDs) == 0:
		return fmt.Errorf("%w: at least one channel is required", ErrInvalidRotation)
	case r.CurrentIndex < 0 || r.CurrentIndex >= len(r.ChannelIDs):
		return fmt.Errorf("%w: current_index must identify a channel in the rotation", ErrInvalidRotation)
	case (r.OverrideChannelID == nil) != (r.OverrideUntil == nil):
		return fmt.Errorf("%w: override channel and expiry must be set or cleared together", ErrInvalidRotation)
	case r.OverrideChannelID != nil && (*r.OverrideChannelID <= 0 || r.OverrideUntil.IsZero()):
		return fmt.Errorf("%w: override channel and expiry must be valid", ErrInvalidRotation)
	}
	seen := make(map[int64]bool, len(r.ChannelIDs))
	for _, id := range r.ChannelIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("%w: channel_ids must contain distinct positive IDs", ErrInvalidRotation)
		}
		seen[id] = true
	}
	return nil
}

func checkRotationChannels(ctx context.Context, tx *sql.Tx, r *OnCallRotation) error {
	ids := slices.Clone(r.ChannelIDs)
	if r.OverrideChannelID != nil && !slices.Contains(ids, *r.OverrideChannelID) {
		ids = append(ids, *r.OverrideChannelID)
	}
	placeholders, args := bulkArgs(ids)
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_channels WHERE id IN ("+placeholders+")", args...).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return fmt.Errorf("%w: unknown notification channel", ErrInvalidRotation)
	}
	return nil
}

func rotationOverrideUntil(r *OnCallRotation) any {
	if r.OverrideUntil != nil {
		return formatTime(*r.OverrideUntil)
	}
	return nil
}

// Project elapsed UTC periods without changing the original cadence. Reads
// apply this too, so late worker ticks and restarts never return a stale contact.
// Overrides temporarily replace the contact without pausing the rotation.
func projectRotation(r *OnCallRotation, now time.Time) {
	if len(r.ChannelIDs) == 0 {
		r.CurrentIndex = 0
		return
	}
	r.CurrentIndex = (r.CurrentIndex%len(r.ChannelIDs) + len(r.ChannelIDs)) % len(r.ChannelIDs)
	if r.PeriodStartedAt.IsZero() {
		r.PeriodStartedAt = r.UpdatedAt
	}
	if r.PeriodStartedAt.IsZero() {
		r.PeriodStartedAt = now.UTC()
	}
	periodSecs := int64(86400)
	if r.Period == "weekly" {
		periodSecs *= 7
	}
	if elapsed := now.Unix() - r.PeriodStartedAt.Unix(); elapsed >= periodSecs {
		steps := elapsed / periodSecs
		r.CurrentIndex = (r.CurrentIndex + int(steps%int64(len(r.ChannelIDs)))) % len(r.ChannelIDs)
		r.PeriodStartedAt = time.Unix(r.PeriodStartedAt.Unix()+steps*periodSecs, 0).UTC()
	}
}
