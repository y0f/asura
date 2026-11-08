package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestCheckStorageSummariesExcludePrivateColumns(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	mon := &Monitor{Name: "monitor", Type: "http", Interval: 60, Timeout: 10}
	if err := s.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}
	check := &CheckResult{MonitorID: mon.ID, Status: "down", ResponseTime: 12, StatusCode: 503,
		Message: "private-message", Headers: "private-headers", Body: strings.Repeat("private-body", 10000), BodyHash: "private-hash", CertFingerprint: "private-fingerprint", DNSRecords: "private-dns"}
	if err := s.InsertCheckResult(ctx, check); err != nil {
		t.Fatal(err)
	}
	change := &ContentChange{MonitorID: mon.ID, OldHash: "private-old", NewHash: "private-new", Diff: strings.Repeat("private-diff", 10000)}
	if err := s.InsertContentChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	access := MonitorAccess{MonitorIDs: []int64{mon.ID}}
	page := Pagination{Page: 1, PerPage: 10}
	for _, full := range []bool{false, true} {
		list, latest, changes := s.ListCheckSummaries, s.GetLatestCheckSummary, s.ListContentChangeSummaries
		if full {
			list, latest, changes = s.ListCheckResults, s.GetLatestCheckResult, s.ListContentChanges
		}
		result, err := list(ctx, mon.ID, page, access)
		if err != nil {
			t.Fatal(err)
		}
		last, err := latest(ctx, mon.ID, access)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []*CheckResult{result.Data.([]*CheckResult)[0], last} {
			if row.ID != check.ID || row.StatusCode != 503 || row.ResponseTime != 12 {
				t.Fatal("summary lost measured scalars")
			}
			for field, pair := range map[string][2]string{"message": {row.Message, check.Message}, "headers": {row.Headers, check.Headers}, "body": {row.Body, check.Body}, "hash": {row.BodyHash, check.BodyHash}, "fingerprint": {row.CertFingerprint, check.CertFingerprint}, "dns": {row.DNSRecords, check.DNSRecords}} {
				want := ""
				if full {
					want = pair[1]
				}
				if pair[0] != want {
					t.Fatalf("full=%t storage %s projection incorrect", full, field)
				}
			}
		}
		result, err = changes(ctx, mon.ID, page, access)
		if err != nil {
			t.Fatal(err)
		}
		got := result.Data.([]*ContentChange)[0]
		if got.ID != change.ID || (got.Diff == change.Diff) != full || (got.OldHash != "") != full || (got.NewHash != "") != full {
			t.Fatal("content summary loaded private fields or lost history")
		}
		if _, err := list(ctx, mon.ID, page, MonitorAccess{}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("empty scope admitted query: %v", err)
		}
	}
}

func TestOpenIncidentCarriesCurrentOwnerForDiagnosticProjection(t *testing.T) {
	s, groups, monitors := accessFixture(t)
	ctx := context.Background()
	mon := monitors[0]
	if err := s.CreateIncident(ctx, &Incident{MonitorID: mon.ID, Status: "open", Cause: "private-cause"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetOpenIncident(ctx, mon.ID, MonitorAccess{GroupIDs: []int64{groups[0].ID}})
	if err != nil || before.MonitorGroupID == nil || *before.MonitorGroupID != groups[0].ID {
		t.Fatalf("owner snapshot missing: %+v, %v", before, err)
	}
	if _, err := s.BulkSetMonitorGroup(ctx, []int64{mon.ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOpenIncident(ctx, mon.ID, MonitorAccess{GroupIDs: []int64{groups[0].ID}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old group retained read: %v", err)
	}
	after, err := s.GetOpenIncident(ctx, mon.ID, MonitorAccess{GroupIDs: []int64{groups[1].ID}})
	if err != nil || after.MonitorGroupID == nil || *after.MonitorGroupID != groups[1].ID {
		t.Fatalf("current owner not returned: %+v, %v", after, err)
	}
}
