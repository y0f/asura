package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProxyUpdate runs inside the writer transaction. It must be pure and must not
// call the store or perform I/O. The current password is decrypted internally.
type ProxyUpdate func(current *Proxy) (*Proxy, error)

var ErrProxyPasswordStorage = errors.New("passwords starting with the reserved storage prefix require database encryption")
var ErrProxyRoutingForbidden = errors.New("changing proxy routing requires update and assignment authority for every monitor using it")

func (s *SQLiteStore) encryptProxyPassword(value string) (string, error) {
	if s.enc == nil && strings.HasPrefix(value, encPrefix) {
		return "", ErrProxyPasswordStorage
	}
	return s.encryptSettings(value)
}

func (s *SQLiteStore) scanProxy(scan func(...any) error) (*Proxy, error) {
	var p Proxy
	var createdAt, updatedAt, authPass string
	if err := scan(&p.ID, &p.Name, &p.Protocol, &p.Host, &p.Port, &p.AuthUser, &authPass, &p.Enabled, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var err error
	p.AuthPass, err = s.decryptRequiredSecret(authPass)
	if err != nil {
		return nil, fmt.Errorf("decrypt proxy password: %w", err)
	}
	p.CreatedAt, p.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return &p, nil
}

func (s *SQLiteStore) UpdateProxyWith(ctx context.Context, id int64, prepare ProxyUpdate, access ...MonitorAccess) (*Proxy, error) {
	if prepare == nil {
		return nil, fmt.Errorf("missing proxy update")
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := s.scanProxy(tx.QueryRowContext(ctx, "SELECT "+proxyColumns+" FROM proxies WHERE id=?", id).Scan)
	if err != nil {
		return nil, err
	}
	input := *current
	candidate, err := prepare(&input)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, fmt.Errorf("missing prepared proxy")
	}
	next := *candidate
	next.ID, next.CreatedAt = id, current.CreatedAt
	next.UpdatedAt = parseTime(formatTime(time.Now()))
	if next.Protocol != current.Protocol || next.Host != current.Host || next.Port != current.Port || next.AuthUser != current.AuthUser || next.AuthPass != current.AuthPass {
		// A proxy can observe its monitors' requests. Owning the proxy resource
		// alone must not authorize forwarding another scope's credentials.
		clause, args := monitorAccessSQL("m", access)
		var forbidden bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM monitors m WHERE m.proxy_id=? AND NOT COALESCE((1=1"+clause+"), FALSE))", append([]any{id}, args...)...).Scan(&forbidden); err != nil {
			return nil, err
		}
		if forbidden {
			return nil, ErrProxyRoutingForbidden
		}
	}
	if next.Protocol != current.Protocol {
		if err := s.checkProxyMonitors(ctx, tx, id, next.Protocol); err != nil {
			return nil, err
		}
	}
	password, err := s.encryptProxyPassword(next.AuthPass)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE proxies SET name=?, protocol=?, host=?, port=?, auth_user=?, auth_pass=?, enabled=?, updated_at=? WHERE id=?`, next.Name, next.Protocol, next.Host, next.Port, next.AuthUser, password, boolToInt(next.Enabled), formatTime(next.UpdatedAt), id)
	if err != nil {
		return nil, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if count != 1 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &next, nil
}
