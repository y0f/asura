package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateOnCallRotation(ctx context.Context, r *OnCallRotation) error {
	channelIDs, _ := json.Marshal(r.ChannelIDs)
	now := formatTime(time.Now())
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO on_call_rotations (name, channel_ids, period, current_index, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`,
		r.Name, string(channelIDs), r.Period, now, now)
	if err != nil {
		return fmt.Errorf("create on-call rotation: %w", err)
	}
	r.ID, _ = res.LastInsertId()
	r.CreatedAt = parseTime(now)
	r.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetOnCallRotation(ctx context.Context, id int64) (*OnCallRotation, error) {
	var r OnCallRotation
	var channelIDs, createdAt, updatedAt string
	var overrideID sql.NullInt64
	var overrideUntil sql.NullString
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, channel_ids, period, current_index, override_channel_id, override_until, created_at, updated_at
		 FROM on_call_rotations WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &channelIDs, &r.Period, &r.CurrentIndex, &overrideID, &overrideUntil, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(channelIDs), &r.ChannelIDs)
	if overrideID.Valid {
		oid := overrideID.Int64
		r.OverrideChannelID = &oid
	}
	r.OverrideUntil = parseTimePtr(overrideUntil)
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	return &r, nil
}

func (s *SQLiteStore) ListOnCallRotations(ctx context.Context) ([]*OnCallRotation, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, channel_ids, period, current_index, override_channel_id, override_until, created_at, updated_at
		 FROM on_call_rotations ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rotations []*OnCallRotation
	for rows.Next() {
		var r OnCallRotation
		var channelIDs, createdAt, updatedAt string
		var overrideID sql.NullInt64
		var overrideUntil sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &channelIDs, &r.Period, &r.CurrentIndex, &overrideID, &overrideUntil, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(channelIDs), &r.ChannelIDs)
		if overrideID.Valid {
			oid := overrideID.Int64
			r.OverrideChannelID = &oid
		}
		r.OverrideUntil = parseTimePtr(overrideUntil)
		r.CreatedAt = parseTime(createdAt)
		r.UpdatedAt = parseTime(updatedAt)
		rotations = append(rotations, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if rotations == nil {
		rotations = []*OnCallRotation{}
	}
	return rotations, nil
}

func (s *SQLiteStore) UpdateOnCallRotation(ctx context.Context, r *OnCallRotation) error {
	channelIDs, _ := json.Marshal(r.ChannelIDs)
	now := formatTime(time.Now())
	var overrideUntil any
	if r.OverrideUntil != nil {
		overrideUntil = formatTime(*r.OverrideUntil)
	}
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE on_call_rotations SET name=?, channel_ids=?, period=?, current_index=?,
		 override_channel_id=?, override_until=?, updated_at=? WHERE id=?`,
		r.Name, string(channelIDs), r.Period, r.CurrentIndex,
		r.OverrideChannelID, overrideUntil, now, r.ID)
	return err
}

func (s *SQLiteStore) DeleteOnCallRotation(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM on_call_rotations WHERE id=?", id)
	return err
}

func (s *SQLiteStore) AdvanceRotation(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx,
		`UPDATE on_call_rotations SET current_index = (current_index + 1) %
		 MAX(json_array_length(channel_ids), 1), updated_at=? WHERE id=?`,
		formatTime(time.Now()), id)
	return err
}

// CurrentOnCallChannelID returns the channel ID that is currently on call.
func (r *OnCallRotation) CurrentOnCallChannelID() int64 {
	now := time.Now().UTC()
	if r.OverrideChannelID != nil && r.OverrideUntil != nil && now.Before(*r.OverrideUntil) {
		return *r.OverrideChannelID
	}
	if len(r.ChannelIDs) == 0 {
		return 0
	}
	idx := r.CurrentIndex % len(r.ChannelIDs)
	return r.ChannelIDs[idx]
}
