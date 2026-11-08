package storage

import (
	"context"
	"fmt"
	"strings"
)

// FinalizeLegacyAgentAssignments completes schema39's deferred data migration
// after encryption is configured and before serving requests or starting checks.
// The snapshot contains IDs only, so encrypted settings need not be read during
// schema migration. Failure leaves both assignments and pending IDs unchanged.
func (s *SQLiteStore) FinalizeLegacyAgentAssignments(ctx context.Context) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var afterID int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT m.id, m.settings
			FROM legacy_agent_assignments p JOIN monitors m ON m.id=p.id
			WHERE p.kind='monitor' AND p.id>? AND m.agent_enabled=1
			AND m.type NOT IN ('heartbeat','manual','docker','command')
			AND NOT EXISTS (SELECT 1 FROM monitor_agents ma WHERE ma.monitor_id=m.id)
			ORDER BY p.id LIMIT 32`, afterID)
		if err != nil {
			return err
		}
		type candidate struct {
			id       int64
			settings string
		}
		batch := make([]candidate, 0, 32)
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.settings); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if len(batch) == 0 {
			break
		}
		for _, item := range batch {
			afterID = item.id
			if strings.HasPrefix(item.settings, encPrefix) && s.enc == nil {
				return fmt.Errorf("database encryption key is required to migrate existing agent assignments")
			}
			settings, err := s.decryptSettings(item.settings)
			if err != nil {
				return fmt.Errorf("decrypt settings for agent assignment migration: %w", err)
			}
			// False preserves the old implicit-job eligibility rule. Known
			// credential settings remain server-only; custom request data was
			// eligible only through existing explicit assignments, left intact.
			if !agentMayReceiveSettings(settings, false) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_agents (monitor_id, agent_id)
				SELECT ?, a.id FROM legacy_agent_assignments p JOIN agents a ON a.id=p.id
				WHERE p.kind='agent' AND a.enabled=1`, item.id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM legacy_agent_assignments"); err != nil {
		return err
	}
	return tx.Commit()
}
