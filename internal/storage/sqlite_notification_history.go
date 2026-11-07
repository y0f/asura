package storage

import (
	"context"
	"fmt"
	"math"
	"time"
)

func (s *SQLiteStore) InsertNotificationHistory(ctx context.Context, h *NotificationHistory) error {
	_, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO notification_history (channel_id, monitor_id, incident_id, event_type, status, error, sent_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		h.ChannelID,
		h.MonitorID,
		h.IncidentID,
		h.EventType,
		h.Status,
		h.Error,
		formatTime(time.Now()),
	)
	return err
}

func (s *SQLiteStore) ListNotificationHistory(ctx context.Context, f NotifHistoryFilter, p Pagination) (*PaginatedResult, error) {
	where := "1=1"
	clause, args := monitorOwnerAccessSQL("nh.monitor_id", f.Access)
	where += clause

	if f.ChannelID > 0 {
		where += " AND nh.channel_id=?"
		args = append(args, f.ChannelID)
	}
	if f.Status != "" {
		where += " AND nh.status=?"
		args = append(args, f.Status)
	}
	if f.EventType != "" {
		where += " AND nh.event_type=?"
		args = append(args, f.EventType)
	}

	var total int64
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notification_history nh WHERE "+where, countArgs...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count notification history: %w", err)
	}

	offset := (p.Page - 1) * p.PerPage
	args = append(args, p.PerPage, offset)

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT nh.id, nh.channel_id, nc.name, nc.type,
		        nh.monitor_id, COALESCE(m.name, ''), m.group_id,
		        nh.incident_id, nh.event_type, nh.status, nh.error, nh.sent_at
		 FROM notification_history nh
		 JOIN notification_channels nc ON nc.id = nh.channel_id
		 LEFT JOIN monitors m ON m.id = nh.monitor_id
		 WHERE `+where+`
		 ORDER BY nh.sent_at DESC
		 LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list notification history: %w", err)
	}
	defer rows.Close()

	var entries []*NotificationHistory
	for rows.Next() {
		var h NotificationHistory
		var sentAt string
		var monitorID *int64
		var monitorName string
		var incidentID *int64
		if err := rows.Scan(
			&h.ID, &h.ChannelID, &h.ChannelName, &h.ChannelType,
			&monitorID, &monitorName, &h.MonitorGroupID,
			&incidentID, &h.EventType, &h.Status, &h.Error, &sentAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification history: %w", err)
		}
		if monitorID != nil {
			h.MonitorID = monitorID
			h.MonitorName = monitorName
		}
		h.IncidentID = incidentID
		h.SentAt = parseTime(sentAt)
		entries = append(entries, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &PaginatedResult{
		Data:       entries,
		Total:      total,
		Page:       p.Page,
		PerPage:    p.PerPage,
		TotalPages: int(math.Ceil(float64(total) / float64(p.PerPage))),
	}, nil
}
