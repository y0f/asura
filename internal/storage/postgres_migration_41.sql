-- Existing observations cannot prove their original monitor type. Preserve
-- every row as unknown; only newly recorded observations carry provenance.
ALTER TABLE check_results ADD COLUMN observation_kind TEXT COLLATE "C" NOT NULL DEFAULT 'unknown'
    CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));
ALTER TABLE monitor_status ADD COLUMN observation_kind TEXT COLLATE "C" NOT NULL DEFAULT 'unknown'
    CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));
ALTER TABLE check_result_hourly ADD COLUMN observation_kind TEXT COLLATE "C" NOT NULL DEFAULT 'unknown'
    CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));
ALTER TABLE check_result_daily ADD COLUMN observation_kind TEXT COLLATE "C" NOT NULL DEFAULT 'unknown'
    CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));

ALTER TABLE check_result_hourly DROP CONSTRAINT check_result_hourly_pkey;
ALTER TABLE check_result_hourly ADD PRIMARY KEY (monitor_id, hour, observation_kind);
ALTER TABLE check_result_daily DROP CONSTRAINT check_result_daily_pkey;
ALTER TABLE check_result_daily ADD PRIMARY KEY (monitor_id, day, observation_kind);

-- Cached anomaly baselines also lack provenance. Rebuild them from new probe
-- observations; keep the recorded status, timestamps and raw history intact.
UPDATE monitor_status SET baseline_avg=0, baseline_stddev=0, baseline_updated_at=NULL;
