package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const proxyColumns = "id, name, protocol, host, port, auth_user, auth_pass, enabled, created_at, updated_at"

func (s *SQLiteStore) CreateProxy(ctx context.Context, p *Proxy) error {
	now := formatTime(time.Now())
	authPass, err := s.encryptProxyPassword(p.AuthPass)
	if err != nil {
		return fmt.Errorf("encrypt proxy auth: %w", err)
	}
	var id int64
	err = s.writeDB.QueryRowContext(ctx,
		`INSERT INTO proxies (name, protocol, host, port, auth_user, auth_pass, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		p.Name, p.Protocol, p.Host, p.Port, p.AuthUser, authPass, boolToInt(p.Enabled), now, now).Scan(&id)
	if err != nil {
		return err
	}
	p.ID = id
	p.CreatedAt = parseTime(now)
	p.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetProxy(ctx context.Context, id int64) (*Proxy, error) {
	return s.scanProxy(s.readDB.QueryRowContext(ctx, "SELECT "+proxyColumns+" FROM proxies WHERE id=?", id).Scan)
}

func (s *SQLiteStore) ListProxies(ctx context.Context) ([]*Proxy, error) {
	rows, err := s.readDB.QueryContext(ctx,
		"SELECT "+proxyColumns+" FROM proxies ORDER BY "+s.readDB.dialect.noCase("name"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proxies []*Proxy
	for rows.Next() {
		p, err := s.scanProxy(rows.Scan)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if proxies == nil {
		proxies = []*Proxy{}
	}
	return proxies, nil
}

func (s *SQLiteStore) UpdateProxy(ctx context.Context, p *Proxy) error {
	_, err := s.UpdateProxyWith(ctx, p.ID, func(*Proxy) (*Proxy, error) { return p, nil })
	return err
}

func (s *SQLiteStore) DeleteProxy(ctx context.Context, id int64) error {
	result, err := s.writeDB.ExecContext(ctx, "DELETE FROM proxies WHERE id=?", id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
