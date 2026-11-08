package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestProxyUpdateUsesCurrentWriterRowAndPreservesIdentity(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := &Proxy{Name: "original", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "old-secret"}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			current.AuthPass = "new-secret"
			return current, nil
		})
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first update did not enter its transaction")
	}
	second := make(chan error, 1)
	go func() {
		_, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
			if current.AuthPass != "new-secret" {
				return nil, errors.New("metadata edit used stale credentials")
			}
			current.ID = p.ID + 99
			current.CreatedAt = time.Time{}
			current.Name = "renamed"
			return current, nil
		})
		second <- err
	}()
	close(release)
	for _, result := range []<-chan error{first, second} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetProxy(ctx, p.ID)
	if err != nil || got.Name != "renamed" || got.AuthPass != "new-secret" || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("concurrent changes or immutable identity were lost: %v", err)
	}
}

func TestProxyUpdateRollbackAndMissingRows(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Proxy{Name: "original", Protocol: "http", Host: "proxy.test", Port: 8080, AuthPass: "saved-secret"}
	if err := s.CreateProxy(ctx, p); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("rejected replacement")
	if _, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
		current.Name, current.AuthPass = "changed", "changed-secret"
		return current, failure
	}); !errors.Is(err, failure) {
		t.Fatalf("callback failure = %v", err)
	}
	if _, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
		current.AuthPass = "enc:literal"
		return current, nil
	}); !errors.Is(err, ErrProxyPasswordStorage) {
		t.Fatalf("reserved prefix without encryption = %v", err)
	}
	got, err := s.GetProxy(ctx, p.ID)
	if err != nil || got.Name != p.Name || got.AuthPass != p.AuthPass || !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatal("failed update changed the stored proxy")
	}
	if err := s.DeleteProxy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProxy(ctx, p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delete = %v", err)
	}
	called := false
	if _, err := s.UpdateProxyWith(ctx, p.ID, func(current *Proxy) (*Proxy, error) {
		called = true
		return current, nil
	}); !errors.Is(err, sql.ErrNoRows) || called {
		t.Fatal("missing row was passed to preparation")
	}
}
