package httputil

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/pageauth"
	"github.com/y0f/asura/v3/internal/storage"
)

func TestStatusPagePreparedPasswordIsPrivateAndMatchesCurrentInput(t *testing.T) {
	password := "first page password"
	in := StatusPageInput{Password: &password}
	if err := in.PreparePassword(); err != nil {
		t.Fatal(err)
	}
	preparedHash := in.preparedHash
	body, err := json.Marshal(in)
	if err != nil || strings.Contains(string(body), preparedHash) {
		t.Fatalf("private prepared hash entered JSON: %v", err)
	}
	current := &storage.StatusPage{Title: "Page", Slug: "page", PasswordHash: strings.Repeat("a", 64)}
	for attempt := 0; attempt < 2; attempt++ {
		next, _, err := in.Apply(current, false)
		if err != nil || next.PasswordHash != preparedHash || !next.PasswordEnabled {
			t.Fatalf("prepared hash was not reused: %+v %v", next, err)
		}
	}
	if !pageauth.Verify(preparedHash, password) || current.PasswordHash != strings.Repeat("a", 64) {
		t.Fatal("wrong password prepared or current state mutated")
	}
	password = "replacement page password"
	next, _, err := in.Apply(current, false)
	if err != nil || next.PasswordHash == preparedHash || !pageauth.Verify(next.PasswordHash, password) {
		t.Fatalf("changed input reused stale password preparation: %v", err)
	}
}

func TestStatusPagePasswordPreparationPreservesOmissionAndExplicitClear(t *testing.T) {
	current := &storage.StatusPage{Title: "Page", Slug: "page", PasswordHash: strings.Repeat("b", 64)}
	for _, clear := range []bool{false, true} {
		in := StatusPageInput{}
		if clear {
			empty := ""
			in.Password = &empty
		}
		if err := in.PreparePassword(); err != nil {
			t.Fatal(err)
		}
		next, _, err := in.Apply(current, false)
		if err != nil {
			t.Fatal(err)
		}
		if clear && (next.PasswordHash != "" || next.PasswordEnabled) || !clear && (next.PasswordHash != current.PasswordHash || !next.PasswordEnabled) {
			t.Fatalf("omission/clear semantics changed: clear=%v page=%+v", clear, next)
		}
	}
}

func TestStatusPagePasswordLengthErrorCannotChangeCurrentProtection(t *testing.T) {
	password := strings.Repeat("private", pageauth.MaxPasswordBytes)
	in := StatusPageInput{Password: &password}
	if err := in.PreparePassword(); !errors.Is(err, ErrInvalidStatusPage) || !errors.Is(err, pageauth.ErrPasswordLength) || strings.Contains(err.Error(), password) {
		t.Fatalf("invalid password error was not safe validation: %v", err)
	}
	current := &storage.StatusPage{Title: "Page", Slug: "page", PasswordHash: strings.Repeat("c", 64)}
	next, _, err := in.Apply(current, false)
	if !errors.Is(err, ErrInvalidStatusPage) || next != nil || current.PasswordHash != strings.Repeat("c", 64) {
		t.Fatal("invalid password changed existing protection")
	}
}
