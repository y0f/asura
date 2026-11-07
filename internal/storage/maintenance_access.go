package storage

import (
	"context"
	"database/sql"
	"slices"
)

// maintenanceAccessSQL requires every affected monitor to pass every action's
// filter. An empty target list means every monitor, requiring global authority.
func maintenanceAccessSQL(access []MonitorAccess) (string, []any) {
	clause, args := monitorAccessSQL("access_monitor", access)
	if clause == "" {
		return "", nil
	}
	return ` AND json_type(monitor_ids)='array' AND json_array_length(monitor_ids)>0
	 AND NOT EXISTS (
	   SELECT 1 FROM json_each(maintenance_windows.monitor_ids) target
	   LEFT JOIN monitors access_monitor ON access_monitor.id=target.value
	   WHERE target.type!='integer' OR access_monitor.id IS NULL OR COALESCE((1=1` + clause + "), 0)=0)", args
}

func checkMaintenanceTargets(ctx context.Context, tx *sql.Tx, ids []int64, access []MonitorAccess) error {
	if len(ids) == 0 {
		for _, filter := range access {
			if !filter.All {
				return sql.ErrNoRows
			}
		}
		return nil
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] <= 0 {
		return sql.ErrNoRows
	}
	placeholders, args := bulkArgs(ids)
	clause, accessArgs := monitorAccessSQL("", access)
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM monitors WHERE id IN ("+placeholders+")"+clause, append(args, accessArgs...)...).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return sql.ErrNoRows
	}
	return nil
}
