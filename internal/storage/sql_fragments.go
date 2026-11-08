package storage

// These fragments accept only SQL expressions owned by this package. Request
// values remain positional arguments; no identifier or expression comes from a
// caller. Dialect differences are explicit at the query that needs them.
func (dialect sqlDialect) noCase(expression string) string {
	if dialect == postgresDialect {
		return `lower(` + expression + ` COLLATE "C") COLLATE "C"`
	}
	return expression + " COLLATE NOCASE"
}

func (dialect sqlDialect) like(expression, pattern string) string {
	if dialect == postgresDialect {
		return expression + ` COLLATE "C" ILIKE ` + pattern
	}
	return expression + " LIKE " + pattern
}

// PostgreSQL rounds numeric-to-integer casts, whereas SQLite truncates. Keep
// the existing integer presentation without narrowing 64-bit counters to int32.
func (dialect sqlDialect) integer(expression string) string {
	if dialect == postgresDialect {
		expression = "TRUNC(" + expression + ")"
	}
	return "CAST(" + expression + " AS BIGINT)"
}

func (dialect sqlDialect) epochSeconds(expression string) string {
	if dialect == postgresDialect {
		return "CAST(EXTRACT(EPOCH FROM CAST(" + expression + " AS TIMESTAMPTZ)) AS BIGINT)"
	}
	return "CAST(strftime('%s', " + expression + ") AS BIGINT)"
}

func (dialect sqlDialect) heartbeatExpired(ping, delay, cutoff string) string {
	if dialect == postgresDialect {
		return "CAST(" + ping + " AS TIMESTAMPTZ) + (" + delay + ") * INTERVAL '1 second' < CAST(" + cutoff + " AS TIMESTAMPTZ)"
	}
	return "datetime(" + ping + ", '+' || (" + delay + ") || ' seconds') < datetime(" + cutoff + ")"
}
