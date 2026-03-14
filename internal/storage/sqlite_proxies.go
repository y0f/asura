package storage

import (
	"context"
	"fmt"
	"time"
)

func (s *SQLiteStore) CreateProxy(ctx context.Context, p *Proxy) error {
	now := formatTime(time.Now())
	authPass, err := s.encryptSettings(p.AuthPass)
	if err != nil {
		return fmt.Errorf("encrypt proxy auth: %w", err)
	}
	res, err := s.writeDB.ExecContext(ctx,
		`INSERT INTO proxies (name, protocol, host, port, auth_user, auth_pass, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Protocol, p.Host, p.Port, p.AuthUser, authPass, boolToInt(p.Enabled), now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	p.ID = id
	p.CreatedAt = parseTime(now)
	p.UpdatedAt = parseTime(now)
	return nil
}

func (s *SQLiteStore) GetProxy(ctx context.Context, id int64) (*Proxy, error) {
	var p Proxy
	var createdAt, updatedAt, authPass string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT id, name, protocol, host, port, auth_user, auth_pass, enabled, created_at, updated_at
		 FROM proxies WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.AuthUser, &authPass, &p.Enabled, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.AuthPass, _ = s.decryptSettings(authPass)
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

func (s *SQLiteStore) ListProxies(ctx context.Context) ([]*Proxy, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT id, name, protocol, host, port, auth_user, auth_pass, enabled, created_at, updated_at
		 FROM proxies ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proxies []*Proxy
	for rows.Next() {
		var p Proxy
		var createdAt, updatedAt, authPass string
		if err := rows.Scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.AuthUser, &authPass, &p.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.AuthPass, _ = s.decryptSettings(authPass)
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		proxies = append(proxies, &p)
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
	now := formatTime(time.Now())
	authPass, err := s.encryptSettings(p.AuthPass)
	if err != nil {
		return fmt.Errorf("encrypt proxy auth: %w", err)
	}
	_, err = s.writeDB.ExecContext(ctx,
		`UPDATE proxies SET name=?, protocol=?, host=?, port=?, auth_user=?, auth_pass=?, enabled=?, updated_at=? WHERE id=?`,
		p.Name, p.Protocol, p.Host, p.Port, p.AuthUser, authPass, boolToInt(p.Enabled), now, p.ID)
	return err
}

func (s *SQLiteStore) DeleteProxy(ctx context.Context, id int64) error {
	_, err := s.writeDB.ExecContext(ctx, "DELETE FROM proxies WHERE id=?", id)
	return err
}
