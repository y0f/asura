package storage

import (
	"context"
	"database/sql"
	"slices"
)

// maintenanceAccessSQL requires every affected monitor to pass every action's
// filter. An empty target list means every monitor, requiring global authority.
func (dialect sqlDialect) maintenanceAccessSQL(access []MonitorAccess) (string, []any) {
	clause, args := monitorAccessSQL("access_monitor", access)
	if clause == "" {
		return "", nil
	}
	if dialect == postgresDialect {
		return ` AND json_typeof(CAST(monitor_ids AS json))='array'
		 AND json_array_length(CAST(monitor_ids AS json))>0 AND NOT EXISTS (
		   SELECT 1 FROM json_array_elements(CAST(maintenance_windows.monitor_ids AS json)) target(value)
		   LEFT JOIN monitors access_monitor ON access_monitor.id = CASE
		     WHEN json_typeof(target.value)='number' AND CAST(target.value AS text) ~ '^-?[0-9]+$'
		     THEN CAST(CAST(target.value AS text) AS numeric) END
		   WHERE access_monitor.id IS NULL OR NOT COALESCE((1=1` + clause + `), FALSE))`, args
	}
	return ` AND json_type(monitor_ids)='array' AND json_array_length(monitor_ids)>0
	 AND NOT EXISTS (
	   SELECT 1 FROM json_each(maintenance_windows.monitor_ids) target
	   LEFT JOIN monitors access_monitor ON access_monitor.id=target.value
	   WHERE target.type!='integer' OR access_monitor.id IS NULL OR NOT COALESCE((1=1` + clause + "), FALSE))", args
}

func checkMaintenanceTargets(ctx context.Context, tx *sqlTransaction, ids []int64, access []MonitorAccess) error {
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
