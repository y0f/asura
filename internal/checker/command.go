package checker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type CommandChecker struct {
	Allowlist []string
}

func (c *CommandChecker) Type() string { return "command" }

func (c *CommandChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.CommandSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	if settings.Command == "" {
		return &Result{Status: "down", Message: "no command specified"}, nil
	}

	// Security: validate command against allowlist (deny all if empty)
	if len(c.Allowlist) == 0 {
		return &Result{
			Status:  "down",
			Message: "command execution disabled: no allowlist configured",
		}, nil
	}

	// Resolve to canonical path to prevent traversal attacks
	cleanCmd := filepath.Clean(settings.Command)
	allowed := false
	for _, prefix := range c.Allowlist {
		cleanPrefix := filepath.Clean(prefix)
		if cleanCmd == cleanPrefix {
			allowed = true
			break
		}
	}
	if !allowed {
		return &Result{
			Status:  "down",
			Message: fmt.Sprintf("command not in allowlist: %s", settings.Command),
		}, nil
	}
	settings.Command = cleanCmd

	// Args are admin-controlled by design and passed directly to exec without a
	// shell, so there is no shell-injection vector. Reject NUL and newline as a
	// minimal hardening measure against malformed or smuggled arguments.
	for _, arg := range settings.Args {
		if strings.ContainsAny(arg, "\x00\n\r") {
			return &Result{
				Status:  "down",
				Message: "command argument contains illegal control character",
			}, nil
		}
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, settings.Command, settings.Args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			StatusCode:   exitCode,
			Message:      fmt.Sprintf("command failed: %v", err),
			Body:         strings.TrimSpace(stderr.String()),
		}, nil
	}

	return &Result{
		Status:       "up",
		ResponseTime: elapsed,
		StatusCode:   0,
		Message:      "command succeeded",
		Body:         strings.TrimSpace(stdout.String()),
	}, nil
}
