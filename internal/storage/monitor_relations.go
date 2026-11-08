package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

type monitorWriteExecutor interface {
	insertIDExecutor
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

var ErrInvalidMonitorRelation = errors.New("a referenced monitor resource does not exist")

func monitorRelationError(err error) error {
	// SQLite's extended SQLITE_CONSTRAINT_FOREIGNKEY code is 787. Match the
	// structured result, never error strings or unrelated constraint failures.
	// https://www.sqlite.org/rescode.html#constraint_foreignkey
	var coded interface{ Code() int }
	if errors.As(err, &coded) && coded.Code() == 787 {
		return fmt.Errorf("%w: %w", ErrInvalidMonitorRelation, err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "23503" {
		return fmt.Errorf("%w: %w", ErrInvalidMonitorRelation, err)
	}
	return err
}

// The legacy group_id column has no FK. Call under the writer transaction so
// deletion cannot race admission; DeleteMonitorGroup clears memberships under
// that same writer lock before deleting its group.
func checkMonitorGroup(ctx context.Context, tx *sqlTransaction, groupID *int64) error {
	if groupID == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM monitor_groups WHERE id=?)", *groupID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidMonitorRelation
	}
	return nil
}

// MonitorCreateOptions carries dependent state that must be created with a
// monitor. Heartbeat credentials are hashed by storage, and caller-owned output
// values are populated only after the complete transaction commits.
type MonitorCreateOptions struct {
	Heartbeat *Heartbeat
}

// MonitorRelationChanges identifies explicitly authorized changes. Unselected
// fields/relationships retain their current transactional values, not a form's
// stale snapshot. Enabled is separate from configuration editing authority.
type MonitorRelationChanges struct {
	Group, Proxy, EscalationPolicy, Notifications, Tags, Agents, Enabled bool
}

// MonitorUpdate must be pure and perform no I/O: the single writer transaction
// is held while it validates and prepares a replacement from current values.
type MonitorUpdate func(current *Monitor) (*Monitor, MonitorRelationChanges, error)

// MonitorClone has the same pure/no-I/O requirement as MonitorUpdate. Prepare
// any new random credential before entering the transaction.
type MonitorClone func(current *Monitor) (*Monitor, MonitorCreateOptions, error)

func (s *SQLiteStore) CloneMonitorWithRelations(ctx context.Context, id int64, prepare MonitorClone, source, destination []MonitorAccess) (*Monitor, *Heartbeat, error) {
	if prepare == nil {
		return nil, nil, fmt.Errorf("monitor clone preparation is required")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	clause, args := monitorAccessSQL("m", source)
	current, err := s.scanMonitor(tx.QueryRowContext(ctx, selectMonitorSQL+" WHERE m.id=?"+clause, append([]any{id}, args...)...))
	if err != nil {
		return nil, nil, err
	}
	if err := loadMonitorRelations(ctx, tx, current); err != nil {
		return nil, nil, err
	}
	clone, options, err := prepare(current)
	if err != nil {
		return nil, nil, err
	}
	if clone == nil {
		return nil, nil, fmt.Errorf("monitor clone preparation returned no monitor")
	}
	saved, heartbeat, err := s.insertMonitor(ctx, tx, clone, &options, destination...)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return saved, heartbeat, nil
}

// UpdateMonitorWithRelations authorizes and prepares against one current
// transactional snapshot, then writes fields and selected relationships together.
// A preparation, encryption, relationship or commit failure returns no monitor.
func (s *SQLiteStore) UpdateMonitorWithRelations(ctx context.Context, id int64, prepare MonitorUpdate, access ...MonitorAccess) (*Monitor, error) {
	if prepare == nil {
		return nil, fmt.Errorf("monitor update preparation is required")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	clause, args := monitorAccessSQL("m", access)
	current, err := s.scanMonitor(tx.QueryRowContext(ctx, selectMonitorSQL+" WHERE m.id=?"+clause, append([]any{id}, args...)...))
	if err != nil {
		return nil, err
	}
	if err := loadMonitorRelations(ctx, tx, current); err != nil {
		return nil, err
	}
	original := copyMonitorSnapshot(current)
	m, changes, err := prepare(current)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("monitor update preparation returned no monitor")
	}
	// Do not mutate a caller-owned candidate on failed writes.
	m = copyMonitorSnapshot(m)
	m.ID, m.CreatedAt = id, original.CreatedAt
	if !changes.Group {
		m.GroupID = original.GroupID
	}
	if !changes.Proxy {
		m.ProxyID = original.ProxyID
	}
	if !changes.EscalationPolicy {
		m.EscalationPolicyID = original.EscalationPolicyID
	}
	if !changes.Notifications {
		m.NotificationChannelIDs = original.NotificationChannelIDs
	}
	if !changes.Tags {
		m.MonitorTags = original.MonitorTags
	}
	if !changes.Agents {
		m.AgentIDs, m.AgentEnabled = original.AgentIDs, original.AgentEnabled
	}
	if !changes.Enabled {
		m.Enabled = original.Enabled
	}
	if err := s.updateMonitor(ctx, tx, m, access...); err != nil {
		return nil, err
	}
	if err := replaceMonitorRelations(ctx, tx, m, changes); err != nil {
		return nil, err
	}
	// Return persisted configuration and computed status, never caller-supplied
	// runtime fields or the paused status of a monitor that was just enabled.
	saved, err := s.scanMonitor(tx.QueryRowContext(ctx, selectMonitorSQL+" WHERE m.id=?", id))
	if err != nil {
		return nil, err
	}
	saved.NotificationChannelIDs, saved.MonitorTags, saved.AgentIDs = m.NotificationChannelIDs, m.MonitorTags, m.AgentIDs
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

func copyMonitorSnapshot(m *Monitor) *Monitor {
	copy := *m
	copy.Settings = slices.Clone(m.Settings)
	copy.Assertions = slices.Clone(m.Assertions)
	copy.Tags = slices.Clone(m.Tags)
	copy.NotificationChannelIDs = slices.Clone(m.NotificationChannelIDs)
	copy.MonitorTags = slices.Clone(m.MonitorTags)
	copy.AgentIDs = slices.Clone(m.AgentIDs)
	copyID := func(id *int64) *int64 {
		if id == nil {
			return nil
		}
		value := *id
		return &value
	}
	copy.GroupID, copy.ProxyID, copy.EscalationPolicyID = copyID(m.GroupID), copyID(m.ProxyID), copyID(m.EscalationPolicyID)
	return &copy
}

func loadMonitorRelations(ctx context.Context, tx *sqlTransaction, m *Monitor) error {
	readIDs := func(query string) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, query, m.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	var err error
	m.NotificationChannelIDs, err = readIDs("SELECT channel_id FROM monitor_notifications WHERE monitor_id=? ORDER BY channel_id")
	if err != nil {
		return err
	}
	m.AgentIDs, err = readIDs("SELECT agent_id FROM monitor_agents WHERE monitor_id=? ORDER BY agent_id")
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT tag_id, value FROM monitor_tags WHERE monitor_id=? ORDER BY tag_id", m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tag MonitorTag
		if err := rows.Scan(&tag.TagID, &tag.Value); err != nil {
			return err
		}
		m.MonitorTags = append(m.MonitorTags, tag)
	}
	return rows.Err()
}

func replaceMonitorRelations(ctx context.Context, tx *sqlTransaction, m *Monitor, changes MonitorRelationChanges) error {
	for _, relation := range []struct {
		table string
		apply bool
	}{{"monitor_notifications", changes.Notifications}, {"monitor_tags", changes.Tags}, {"monitor_agents", changes.Agents}} {
		if !relation.apply {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+relation.table+" WHERE monitor_id=?", m.ID); err != nil {
			return err
		}
	}
	if changes.Notifications {
		for _, channelID := range m.NotificationChannelIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO monitor_notifications (monitor_id, channel_id) VALUES (?, ?)", m.ID, channelID); err != nil {
				return monitorRelationError(err)
			}
		}
	}
	if changes.Tags {
		for _, tag := range m.MonitorTags {
			if _, err := tx.ExecContext(ctx, "INSERT INTO monitor_tags (monitor_id, tag_id, value) VALUES (?, ?, ?)", m.ID, tag.TagID, tag.Value); err != nil {
				return monitorRelationError(err)
			}
		}
	}
	if changes.Agents {
		for _, agentID := range m.AgentIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO monitor_agents (monitor_id, agent_id) VALUES (?, ?)", m.ID, agentID); err != nil {
				return monitorRelationError(err)
			}
		}
	}
	return nil
}
