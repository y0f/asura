package checker

import (
	"context"
	"fmt"
	"sync"

	"github.com/y0f/asura/internal/storage"
)

// Result holds the outcome of a protocol check.
type Result struct {
	Status          string // "up", "down", "degraded"
	ResponseTime    int64  // milliseconds
	StatusCode      int
	Message         string
	Headers         map[string]string
	Body            string
	BodyHash        string
	CertExpiry      *int64 // unix timestamp
	CertFingerprint string // SHA-256 hex fingerprint of leaf cert
	DNSRecords      []string
}

// Checker performs a protocol-specific check against a target.
type Checker interface {
	// Type returns the protocol type this checker handles.
	Type() string
	// Check performs the health check.
	Check(ctx context.Context, monitor *storage.Monitor) (*Result, error)
}

// Registry holds all registered checkers by type.
type Registry struct {
	mu       sync.RWMutex
	checkers map[string]Checker
}

func NewRegistry() *Registry {
	return &Registry{checkers: make(map[string]Checker)}
}

func (r *Registry) Register(c Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[c.Type()] = c
}

func (r *Registry) Get(typ string) (Checker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.checkers[typ]
	if !ok {
		return nil, fmt.Errorf("no checker registered for type: %s", typ)
	}
	return c, nil
}

// DefaultRegistry creates a registry with all built-in checkers.
func DefaultRegistry(commandAllowlist []string, allowPrivateTargets bool) *Registry {
	r := NewRegistry()
	r.Register(&HTTPChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&TCPChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&DNSChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&ICMPChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&TLSChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&WebSocketChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&CommandChecker{Allowlist: commandAllowlist})
	r.Register(&DockerChecker{})
	r.Register(&DomainChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&GRPCChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&MQTTChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&SMTPChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&SSHChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&RedisChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&PostgreSQLChecker{AllowPrivate: allowPrivateTargets})
	r.Register(&UDPChecker{AllowPrivate: allowPrivateTargets})
	return r
}
