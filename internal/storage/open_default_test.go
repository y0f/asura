//go:build !postgres

package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDefaultBuildRejectsUnavailableBackendWithoutExposingDSN(t *testing.T) {
	store, err := Open(context.Background(), Options{Driver: "postgres", DSN: "postgres://user:private-password@localhost/database", MaxReadConns: 1})
	if store != nil || !errors.Is(err, ErrBackendUnavailable) || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("default build accepted unavailable backend or exposed credentials: %v", err)
	}
}
