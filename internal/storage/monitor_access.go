package storage

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

// MonitorAccess is a storage filter, not a role or identity. Its zero value
// denies every monitor. Within one filter, IDs and current group membership are
// alternatives; multiple filters are intersected, preserving action boundaries.
// Omit filters only for trusted internal operations such as the check scheduler.
type MonitorAccess struct {
	All        bool
	MonitorIDs []int64
	GroupIDs   []int64
}

func (a MonitorAccess) allows(id int64, groupID *int64) bool {
	return a.All || (id > 0 && slices.Contains(a.MonitorIDs, id)) ||
		(groupID != nil && *groupID > 0 && slices.Contains(a.GroupIDs, *groupID))
}

// monitorAccessSQL accepts only source-code table aliases, never request input.
// The leading AND makes it suitable for adding to an existing WHERE clause.
func monitorAccessSQL(alias string, access []MonitorAccess) (string, []any) {
	if alias != "" {
		alias += "."
	}
	var clauses strings.Builder
	var args []any
	for _, filter := range access {
		if filter.All {
			continue
		}
		if len(filter.MonitorIDs) == 0 && len(filter.GroupIDs) == 0 {
			clauses.WriteString(" AND 0")
			continue
		}
		clauses.WriteString(" AND (")
		if len(filter.MonitorIDs) != 0 {
			placeholders, ids := bulkArgs(filter.MonitorIDs)
			clauses.WriteString(alias + "id IN (" + placeholders + ")")
			args = append(args, ids...)
		}
		if len(filter.GroupIDs) != 0 {
			if len(filter.MonitorIDs) != 0 {
				clauses.WriteString(" OR ")
			}
			placeholders, ids := bulkArgs(filter.GroupIDs)
			clauses.WriteString(alias + "group_id IN (" + placeholders + ")")
			args = append(args, ids...)
		}
		clauses.WriteByte(')')
	}
	return clauses.String(), args
}

func allowedMonitorDestination(id int64, groupID *int64, access []MonitorAccess) bool {
	for _, filter := range access {
		if !filter.allows(id, groupID) {
			return false
		}
	}
	return true
}

type monitorDestination struct {
	groupID *int64
}

// Existing ID grants allow ordinary edits, but moving into another group needs
// authority over that destination group. Test unchanged membership in the same
// statement as the source filter so a concurrent move cannot bypass the guard.
func monitorDestinationSQL(destination *monitorDestination, access []MonitorAccess) (string, []any) {
	if destination == nil || allowedMonitorDestination(0, destination.groupID, access) {
		return "", nil
	}
	return " AND group_id IS ?", []any{destination.groupID}
}

func scopedMutationResult(result sql.Result, err error, access []MonitorAccess) error {
	if err != nil || len(access) == 0 {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

// mutateMonitorSet checks the complete set and writes inside the same SQLite
// transaction. A missing or inaccessible ID prevents every requested mutation.
// statement and its initial arguments come only from the fixed callers below.
func (s *SQLiteStore) mutateMonitorSet(ctx context.Context, ids []int64, access []MonitorAccess, destination *monitorDestination, statement string, args []any) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] <= 0 {
		return 0, sql.ErrNoRows
	}
	placeholders, idArgs := bulkArgs(ids)
	accessClause, accessArgs := monitorAccessSQL("", access)
	destinationClause, destinationArgs := monitorDestinationSQL(destination, access)
	where := "id IN (" + placeholders + ")" + accessClause + destinationClause
	whereArgs := append(idArgs, accessArgs...)
	whereArgs = append(whereArgs, destinationArgs...)
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM monitors WHERE "+where, whereArgs...).Scan(&count); err != nil {
		return 0, err
	}
	if count != len(ids) {
		return 0, sql.ErrNoRows
	}
	result, err := tx.ExecContext(ctx, statement+" WHERE "+where, append(args, whereArgs...)...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
