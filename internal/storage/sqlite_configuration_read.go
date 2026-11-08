package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SnapshotConfiguration reads every configuration table in one consistent
// transaction, with no UI pagination cap and no ignored relationship errors.
func (s *SQLiteStore) SnapshotConfiguration(ctx context.Context) (*ConfigurationSnapshot, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c := &ConfigurationSnapshot{HeartbeatGrace: make(map[int64]int)}
	snapshotAt := time.Now().UTC()
	read := func(query string, scan func(*sql.Rows) error) error {
		return readConfigurationRows(ctx, tx, query, scan)
	}
	steps := []func() error{
		func() error {
			return read("SELECT id,name,sort_order FROM monitor_groups ORDER BY id", func(row *sql.Rows) error {
				g := new(MonitorGroup)
				if err := row.Scan(&g.ID, &g.Name, &g.SortOrder); err != nil {
					return err
				}
				c.Groups = append(c.Groups, g)
				return nil
			})
		},
		func() error {
			return read("SELECT id,name,color FROM tags ORDER BY id", func(row *sql.Rows) error {
				t := new(Tag)
				if err := row.Scan(&t.ID, &t.Name, &t.Color); err != nil {
					return err
				}
				c.Tags = append(c.Tags, t)
				return nil
			})
		},
		func() error {
			return read("SELECT id,name,protocol,host,port,auth_user,auth_pass,enabled FROM proxies ORDER BY id", func(row *sql.Rows) error {
				p := new(Proxy)
				var secret string
				if err := row.Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.AuthUser, &secret, &p.Enabled); err != nil {
					return err
				}
				var err error
				p.AuthPass, err = s.decryptConfigurationSecret(secret)
				if err != nil {
					return fmt.Errorf("decrypt proxy %d: %w", p.ID, err)
				}
				c.Proxies = append(c.Proxies, p)
				return nil
			})
		},
		func() error {
			return read("SELECT id,name,type,enabled,settings,events FROM notification_channels ORDER BY id", func(row *sql.Rows) error {
				ch := new(NotificationChannel)
				var settings, events string
				if err := row.Scan(&ch.ID, &ch.Name, &ch.Type, &ch.Enabled, &settings, &events); err != nil {
					return err
				}
				plain, err := s.decryptConfigurationSecret(settings)
				if err != nil {
					return fmt.Errorf("decrypt channel %d: %w", ch.ID, err)
				}
				ch.Settings = json.RawMessage(plain)
				if !json.Valid(ch.Settings) {
					return fmt.Errorf("invalid channel settings JSON for ID %d", ch.ID)
				}
				if err := json.Unmarshal([]byte(events), &ch.Events); err != nil {
					return fmt.Errorf("decode channel events: %w", err)
				}
				c.Channels = append(c.Channels, ch)
				return nil
			})
		},
		func() error {
			return read("SELECT id,name,description,enabled,repeat FROM escalation_policies ORDER BY id", func(row *sql.Rows) error {
				p := new(EscalationPolicy)
				if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Enabled, &p.Repeat); err != nil {
					return err
				}
				c.EscalationPolicies = append(c.EscalationPolicies, p)
				return nil
			})
		},
		func() error {
			return read(selectMonitorSQL+" ORDER BY m.id", func(row *sql.Rows) error {
				m, err := s.scanMonitor(row)
				if err != nil {
					return err
				}
				if !json.Valid(m.Settings) || !json.Valid(m.Assertions) {
					return fmt.Errorf("invalid monitor settings or assertions JSON for ID %d", m.ID)
				}
				c.Monitors = append(c.Monitors, m)
				return nil
			})
		},
		func() error {
			return read("SELECT "+rotationColumns+" FROM on_call_rotations ORDER BY id", func(row *sql.Rows) error {
				r, err := scanRotation(row, snapshotAt)
				if err != nil {
					return err
				}
				c.Rotations = append(c.Rotations, r)
				return nil
			})
		},
		func() error {
			return read("SELECT "+mwColumns+" FROM maintenance_windows ORDER BY id", func(row *sql.Rows) error {
				m, err := scanMaintenanceWindow(row.Scan)
				if err != nil {
					return err
				}
				c.Maintenance = append(c.Maintenance, m)
				return nil
			})
		},
		func() error {
			return read("SELECT "+statusPageColumns+" FROM status_pages ORDER BY id", func(row *sql.Rows) error {
				p := new(StatusPage)
				if err := scanStatusPage(p, row); err != nil {
					return err
				}
				c.StatusPages = append(c.StatusPages, p)
				return nil
			})
		},
		func() error {
			return read("SELECT page_id,monitor_id,sort_order,group_name FROM status_page_monitors ORDER BY page_id,sort_order,monitor_id", func(row *sql.Rows) error {
				var m StatusPageMonitor
				if err := row.Scan(&m.PageID, &m.MonitorID, &m.SortOrder, &m.GroupName); err != nil {
					return err
				}
				c.StatusPageMonitors = append(c.StatusPageMonitors, m)
				return nil
			})
		},
		func() error {
			return read("SELECT id,name FROM agents ORDER BY id", func(row *sql.Rows) error {
				var a ConfigurationAgent
				if err := row.Scan(&a.ID, &a.Name); err != nil {
					return err
				}
				c.Agents = append(c.Agents, a)
				return nil
			})
		},
		func() error {
			return read("SELECT monitor_id,grace FROM heartbeats ORDER BY monitor_id", func(row *sql.Rows) error {
				var id int64
				var grace int
				if err := row.Scan(&id, &grace); err != nil {
					return err
				}
				c.HeartbeatGrace[id] = grace
				return nil
			})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, fmt.Errorf("snapshot configuration: %w", err)
		}
	}
	monitors := make(map[int64]*Monitor, len(c.Monitors))
	for _, m := range c.Monitors {
		monitors[m.ID] = m
	}
	for _, relation := range []struct {
		query string
		apply func(*Monitor, int64)
	}{
		{"SELECT monitor_id,channel_id FROM monitor_notifications ORDER BY monitor_id,channel_id", func(m *Monitor, id int64) { m.NotificationChannelIDs = append(m.NotificationChannelIDs, id) }},
		{"SELECT monitor_id,agent_id FROM monitor_agents ORDER BY monitor_id,agent_id", func(m *Monitor, id int64) { m.AgentIDs = append(m.AgentIDs, id) }},
	} {
		if err := read(relation.query, func(row *sql.Rows) error {
			var monitorID, id int64
			if err := row.Scan(&monitorID, &id); err != nil {
				return err
			}
			m := monitors[monitorID]
			if m == nil {
				return fmt.Errorf("orphan monitor relationship")
			}
			relation.apply(m, id)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if err := read("SELECT monitor_id,tag_id,value FROM monitor_tags ORDER BY monitor_id,tag_id", func(row *sql.Rows) error {
		var id int64
		var tag MonitorTag
		if err := row.Scan(&id, &tag.TagID, &tag.Value); err != nil {
			return err
		}
		m := monitors[id]
		if m == nil {
			return fmt.Errorf("orphan monitor tag")
		}
		m.MonitorTags = append(m.MonitorTags, tag)
		return nil
	}); err != nil {
		return nil, err
	}
	policies := make(map[int64]*EscalationPolicy, len(c.EscalationPolicies))
	for _, p := range c.EscalationPolicies {
		policies[p.ID] = p
	}
	if err := read("SELECT policy_id,step_order,delay_minutes,notification_channel_ids FROM escalation_policy_steps ORDER BY policy_id,step_order,id", func(row *sql.Rows) error {
		step := new(EscalationPolicyStep)
		var ids string
		if err := row.Scan(&step.PolicyID, &step.StepOrder, &step.DelayMinutes, &ids); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(ids), &step.NotificationChannelIDs); err != nil {
			return err
		}
		p := policies[step.PolicyID]
		if p == nil {
			return fmt.Errorf("orphan escalation step")
		}
		p.Steps = append(p.Steps, step)
		return nil
	}); err != nil {
		return nil, err
	}
	// Reject corrupt references instead of exporting a file that silently loses
	// an association. Source enrollment IDs identify themselves in this snapshot.
	ic := &configurationImport{ctx: ctx, tx: tx, data: c, agents: make(map[int64]int64, len(c.Agents))}
	for _, a := range c.Agents {
		ic.agents[a.ID] = a.ID
	}
	if err := ic.prepareCatalogs("replace"); err != nil {
		return nil, err
	}
	if err := ic.checkReferences(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *SQLiteStore) decryptConfigurationSecret(value string) (string, error) {
	if s.enc == nil && strings.HasPrefix(value, encPrefix) {
		return "", fmt.Errorf("the database encryption key is required to read encrypted configuration")
	}
	return s.decryptSettings(value)
}

func readConfigurationRows(ctx context.Context, tx *sql.Tx, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
