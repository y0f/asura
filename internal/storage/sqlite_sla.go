package storage

import (
	"context"
	"fmt"
)

// ListSLAMonitorIDs is an internal keyset scan. The worker checks current
// configuration again under its monitor lock before computing any budget.
func (s *SQLiteStore) ListSLAMonitorIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("SLA batch limit must be between 1 and 1000")
	}
	rows, err := s.readDB.QueryContext(ctx, `SELECT id FROM monitors
		WHERE id>? AND enabled=1 AND sla_target>0 AND type NOT IN ('manual','heartbeat') ORDER BY id LIMIT ?`, afterID, limit)
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
