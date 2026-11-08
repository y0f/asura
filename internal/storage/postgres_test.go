//go:build postgres

package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func postgresFixtureDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ASURA_TEST_POSTGRES_DSN")
	if raw == "" {
		t.Skip("ASURA_TEST_POSTGRES_DSN is required for the isolated PostgreSQL fixture")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || u.Host == "" {
		t.Fatal("PostgreSQL test fixture requires a postgres URL")
	}
	config, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid PostgreSQL fixture connection settings")
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "asura_contract_" + hex.EncodeToString(random[:])
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatalf("create isolated PostgreSQL fixture: %v", postgresStartupError("test database", err))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated PostgreSQL fixture: %v", postgresStartupError("test cleanup", err))
		}
	})
	u.Path = "/" + name
	return u.String()
}

func postgresFixtureStore(t *testing.T) *SQLStore {
	t.Helper()
	store, err := Open(context.Background(), Options{Driver: "postgres", DSN: postgresFixtureDSN(t), MaxReadConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store.(*SQLStore)
}

func TestPostgresContracts(t *testing.T) {
	if os.Getenv("ASURA_TEST_POSTGRES_DSN") == "" {
		t.Skip("PostgreSQL fixture not configured")
	}
	// Setenv makes Go reject future Parallel use in this suite or its parents.
	// No other package test can overlap a nonparallel top-level test.
	t.Setenv("ASURA_STORAGE_CONTRACT", "postgres")
	contractStoreFactory = postgresFixtureStore
	defer func() { contractStoreFactory = nil }()
	for _, test := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"generated_keys", TestGeneratedKeysIdentifyCommittedRows},
		{"write_guard", TestSQLGuardBlocksExecutionAndCommit},
		{"concurrent_close", TestSQLStoreCloseJoinsConcurrentCallers},
		{"aggregate_semantics", TestSQLAggregateSemantics},
		{"prometheus_snapshot", TestPrometheusSnapshotIncludesEveryMonitorAndOnlyScalarData},
		{"prometheus_failures", TestPrometheusSnapshotPropagatesEveryFailure},
		{"prometheus_cancellation", TestPrometheusSnapshotStopsOnVisitorFailureAndCancellation},
		{"monitors", TestMonitorCRUD},
		{"monitor_revisions", TestMonitorRevisionInvalidatesQueuedChecks},
		{"monitor_delegation", TestMonitorCheckRequiresCurrentDelegation},
		{"monitor_relations", TestMonitorRelationSaveRollsBackEveryField},
		{"monitor_preserved_relations", TestMonitorUpdatePreservesCurrentHiddenRelationships},
		{"monitor_transaction_scope", TestMonitorAtomicSaveChecksSourceAndDestination},
		{"monitor_group_references", TestMissingMonitorGroupRejectsEveryWritePath},
		{"checks", TestCheckResults},
		{"incidents", TestIncidents},
		{"incident_diagnostics", TestIncidentCauseSearchUsesIndependentDiagnosticScope},
		{"diagnostic_owner", TestOpenIncidentCarriesCurrentOwnerForDiagnosticProjection},
		{"check_summaries", TestCheckStorageSummariesExcludePrivateColumns},
		{"incident_scope", TestIncidentAccessFiltersCountsNamesAndCurrentMembership},
		{"incident_transitions", TestIncidentTransitionsUseCurrentScopeAndExactColumns},
		{"analytics", TestAnalytics},
		{"passive_metrics", TestPassiveMetricsDoNotInterpretTransitionsAsSamples},
		{"metric_snapshots", TestMonitorMetricSnapshotsKeepAuthorityTypeAndDataTogether},
		{"metric_rollups", TestMonitorMetricsSnapshotCountsRetainedRollups},
		{"observation_rollups", TestObservationRollupsKeepKindsSeparate},
		{"observation_transitions", TestObservationTypeTransitionsNeverReclassifyHistory},
		{"observation_latency_sort", TestObservationLatencySortIgnoresUnprovenValues},
		{"heartbeats", TestHeartbeatCRUD},
		{"sessions", TestSessionCRUD},
		{"totp", TestTOTPKeyCRUD},
		{"proxies", TestProxyCRUD},
		{"proxy_routing", TestProxyRoutingMutationsAreAtomic},
		{"proxy_scope", TestProxyRoutingRequiresEveryCurrentMonitorScope},
		{"proxy_scope_after_wait", TestProxyRoutingChecksAssignmentsAfterWaitingForWriter},
		{"proxy_import", TestProxyImportRejectsIncompatibleDestinationAndRollsBack},
		{"agent_proxy", TestAgentProxyAssignmentRemainsLocal},
		{"agent_proxy_held_check", TestAgentProxyChangeRejectsHeldResult},
		{"notifications", TestNotificationUpdateUsesCurrentRowAndRollsBack},
		{"notification_errors", TestNotificationUnreadableCredentialAndEventsFailClosed},
		{"tags", TestTags},
		{"tag_mutations", TestTagMutationsReportCurrentRowsAndConflicts},
		{"tag_concurrent", TestTagConcurrentCreateReturnsNameConflict},
		{"request_logs", TestRequestLogBatchInsertAndList},
		{"request_stats", TestRequestLogStats},
		{"request_rollups", TestRequestLogRollup},
		{"request_purge", TestRequestLogPurge},
		{"bulk_enabled", TestBulkSetMonitorsEnabled},
		{"bulk_delete", TestBulkDeleteMonitors},
		{"bulk_group", TestBulkSetMonitorGroup},
		{"monitor_scope", TestMonitorAccessFiltersBeforePaginationAndCount},
		{"monitor_destinations", TestMonitorAccessMutationsCheckCurrentOwnerAndDestination},
		{"bulk_scope", TestBulkMonitorAccessRejectsTheWholeSet},
		{"maintenance_scope", TestMaintenanceAccessRequiresEveryCurrentTarget},
		{"maintenance_destinations", TestMaintenanceUpdateChecksExistingAndReplacementTargets},
		{"maintenance_actions", TestMaintenanceUpdateSeparatesCurrentAndDestinationToggleAuthority},
		{"maintenance_concurrency", TestMaintenanceConcurrentFlipsSerializeWithoutLosingTransitions},
		{"rollup_weights", TestDailyRollupWeightsSamples},
		{"rollup_degraded", TestRollupPreservesDegraded},
		{"rollup_gaps", TestUptimeRollupGapsAndPartialDays},
		{"retention_backlog", TestRetentionSummarizesOldBacklogAndHoles},
		{"retention_legacy", TestLegacyPartialSourcesKeepExistingSummaries},
		{"sla_scan", TestSLAMonitorScanUsesEnabledTargetsAndKeyset},
		{"configuration_roundtrip", TestConfigurationRoundTripRemapsEveryRelationship},
		{"configuration_rollback", TestConfigurationReplacementRollbackIncludesCascades},
		{"configuration_references", TestConfigurationPreflightRejectsInvalidReferencesBeforeWrites},
		{"configuration_merge", TestConfigurationMergePreservesExistingSecretsAndRejectsAmbiguity},
		{"configuration_replace", TestConfigurationReplaceRemovesChannelsButPreservesEnrollments},
		{"configuration_agents", TestConfigurationAgentJobEligibilitySurvivesFullRoundTrip},
		{"configuration_encryption", TestConfigurationEncryptsDestinationSecretsAndFailsWrongKeyExport},
		{"rotation_cadence", TestRotationReadsAndRepeatedAdvancePreserveCadence},
		{"rotation_edits", TestRotationEditsAndOverridesKeepCurrentCadence},
		{"escalation_policy", TestEscalationPolicyCRUD},
		{"escalation_steps", TestEscalationPolicySteps},
		{"escalation_state", TestEscalationState},
		{"escalation_channel_validation", TestEscalationSaveValidatesChannelsBeforeWrites},
		{"heartbeat_expiry", TestHeartbeatExpiryRechecksDeadline},
		{"heartbeat_types", TestHeartbeatTransitionsRejectChangedMonitorType},
		{"manual_transitions", TestManualTransitionCommitsHistoryAndStatus},
		{"manual_scope", TestManualTransitionRequiresCurrentTypeAndEveryScope},
		{"badge_snapshot", TestPublicBadgeDataUsesPublicationSnapshot},
	} {
		t.Run(test.name, test.run)
	}
}

func TestPostgresOwnershipLifecycle(t *testing.T) {
	dsn := postgresFixtureDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	options := Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1, RuntimeOwner: true}
	first, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if second, err := Open(ctx, options); !errors.Is(err, ErrDatabaseOwned) || second != nil {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second runtime acquired ownership: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	var losses atomic.Int32
	lost := make(chan struct{}, 1)
	options.OnOwnershipLoss = func() { losses.Add(1); lost <- struct{}{} }
	reopened, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	store := reopened.(*SQLStore)
	// Exhausting the ordinary reader pool must not prevent the independent
	// observer from verifying the writer's ownership lock.
	busyReader, err := store.readDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-lost:
		busyReader.Close()
		t.Fatal("reader pool saturation falsely lost runtime ownership")
	case <-time.After(4 * time.Second):
	}
	if err := busyReader.Close(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := store.writeDB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := store.readDB.QueryRowContext(ctx, "SELECT pg_terminate_backend(?)", pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate fixture writer: %v", err)
	}
	select {
	case <-lost:
	case <-ctx.Done():
		t.Fatal("ownership loss did not stop runtime")
	}
	if _, err := store.writeDB.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES ('after-loss','forbidden')"); !errors.Is(err, ErrDatabaseOwnershipLost) {
		t.Fatalf("writer resumed after ownership loss: %v", err)
	}
	if losses.Load() != 1 {
		t.Fatalf("ownership loss callback count=%d", losses.Load())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresErrorsDoNotExposeConnectionCredentials(t *testing.T) {
	const secret = "fixture-private-password"
	_, err := Open(context.Background(), Options{Driver: "postgres", DSN: "postgres://user:" + secret + "@invalid-host:invalid-port/database", MaxReadConns: 1})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe connection error: %v", err)
	}
	if !errors.Is(postgresStartupError("test", context.Canceled), context.Canceled) {
		t.Fatal("startup lost cancellation")
	}
}

func TestPostgresReadPoolAndSnapshotIsolation(t *testing.T) {
	store := postgresFixtureStore(t)
	ctx := context.Background()
	if _, err := store.readDB.ExecContext(ctx, "INSERT INTO tags(name) VALUES ('forbidden-reader-write')"); err == nil {
		t.Fatal("read pool accepted a write")
	}
	if err := store.CreateTag(ctx, &Tag{Name: "first", Color: "#111111"}); err != nil {
		t.Fatal(err)
	}
	options := &sql.TxOptions{ReadOnly: true}
	tx, err := store.readDB.BeginTx(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before, after int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tags").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTag(ctx, &Tag{Name: "second", Color: "#222222"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tags").Scan(&after); err != nil || after != before || before != 1 {
		t.Fatalf("read snapshot changed across statements: before=%d after=%d err=%v", before, after, err)
	}
	if options.Isolation != sql.LevelDefault {
		t.Fatal("BeginTx mutated caller options")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.readDB.QueryRowContext(ctx, "SELECT count(*) FROM tags").Scan(&after); err != nil || after != 2 {
		t.Fatalf("next snapshot missed committed write: %d %v", after, err)
	}
}

func TestPostgresCaseOrderingAndNullMaintenance(t *testing.T) {
	s := postgresFixtureStore(t)
	ctx := context.Background()
	var monitors []*Monitor
	for _, name := range []string{"alpha", "Bravo", "Älpha", "älpha", "ALPINE"} {
		monitors = append(monitors, createTestMonitor(t, s, ctx, name))
	}
	page, err := s.ListMonitors(ctx, MonitorListFilter{}, Pagination{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"alpha", "ALPINE", "Bravo", "Älpha", "älpha"} {
		if got := page.Data.([]*Monitor)[i].Name; got != want {
			t.Fatalf("ASCII case ordering[%d]=%q, want%q", i, got, want)
		}
	}
	for query, want := range map[string]int64{"a": 2, "A": 2, "Ä": 1, "ä": 1} {
		page, err := s.ListMonitors(ctx, MonitorListFilter{Search: query}, Pagination{Page: 1, PerPage: 10})
		if err != nil || page.Total != want {
			t.Fatalf("search %q: %+v %v", query, page, err)
		}
	}
	if _, err := s.writeDB.ExecContext(ctx, "UPDATE monitor_status SET last_check_at=? WHERE monitor_id=?", "2026-01-01T00:00:00Z", monitors[2].ID); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListMonitors(ctx, MonitorListFilter{Sort: "last_check"}, Pagination{Page: 1, PerPage: 10})
	if err != nil || page.Data.([]*Monitor)[0].ID != monitors[2].ID {
		t.Fatalf("NULL last-check ordering changed: %+v %v", page, err)
	}
	window := &MaintenanceWindow{Name: "scope", MonitorIDs: []int64{monitors[0].ID}, Recurring: "manual"}
	if err := s.CreateMaintenanceWindow(ctx, window); err != nil {
		t.Fatal(err)
	}
	access := MonitorAccess{MonitorIDs: []int64{monitors[0].ID}}
	for _, raw := range []string{"null", `"scalar"`, "{}", "[]", fmt.Sprintf("[%d.0]", monitors[0].ID), fmt.Sprintf("[%de0]", monitors[0].ID), fmt.Sprintf(`["%d"]`, monitors[0].ID)} {
		if _, err := s.writeDB.ExecContext(ctx, "UPDATE maintenance_windows SET monitor_ids=? WHERE id=?", raw, window.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetMaintenanceWindow(ctx, window.ID, access); !errors.Is(err, sql.ErrNoRows) || got != nil {
			t.Fatalf("invalid target JSON %s widened scope or failed as a server error: %+v %v", raw, got, err)
		}
	}
	if _, err := s.writeDB.ExecContext(ctx, "UPDATE maintenance_windows SET monitor_ids='{' WHERE id=?", window.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetMaintenanceWindow(ctx, window.ID, access); err == nil || got != nil {
		t.Fatalf("malformed JSON must fail closed: %+v %v", got, err)
	}
}

func TestPostgresRefusesExistingAndUnsupportedSchemas(t *testing.T) {
	t.Run("occupied database", func(t *testing.T) {
		dsn := postgresFixtureDSN(t)
		config, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal("invalid fixture config")
		}
		db := stdlib.OpenDB(*config)
		defer db.Close()
		if _, err := db.Exec("CREATE TABLE unrelated_fixture(value text)"); err != nil {
			t.Fatal(err)
		}
		if store, err := Open(context.Background(), Options{Driver: "postgres", DSN: dsn, MaxReadConns: 1}); !errors.Is(err, errPostgresDatabaseNotEmpty) || store != nil {
			if store != nil {
				store.Close()
			}
			t.Fatalf("occupied database accepted: %v", err)
		}
		var present bool
		if err := db.QueryRow("SELECT to_regclass('public.unrelated_fixture') IS NOT NULL").Scan(&present); err != nil || !present {
			t.Fatal("existing fixture table changed")
		}
	})
	t.Run("unsupported version", func(t *testing.T) {
		s := postgresFixtureStore(t)
		if _, err := s.writeDB.Exec("UPDATE schema_version SET version=?", schemaVersion+1); err != nil {
			t.Fatal(err)
		}
		if err := migratePostgres(context.Background(), s.writeDB); err == nil {
			t.Fatal("future schema accepted")
		}
		var got int
		if err := s.readDB.QueryRow("SELECT version FROM schema_version").Scan(&got); err != nil || got != schemaVersion+1 {
			t.Fatalf("future schema mutated: %d %v", got, err)
		}
	})
}
