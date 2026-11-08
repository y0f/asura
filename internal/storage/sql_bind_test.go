package storage

import "testing"

func TestPostgresBindPreservesSQLLiteralsAndComments(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"SELECT ?, ?, ?", "SELECT $1, $2, $3"},
		{"SELECT '?' AS literal, ?", "SELECT '?' AS literal, $1"},
		{"SELECT 'it''s ?' AS literal, ?", "SELECT 'it''s ?' AS literal, $1"},
		{`SELECT "?", "double""?", ` + "`?`" + `, ?`, `SELECT "?", "double""?", ` + "`?`" + `, $1`},
		{"SELECT ? -- ignored ?\n, ?", "SELECT $1 -- ignored ?\n, $2"},
		{"SELECT ? /* ? /* ? */ ? */, ?", "SELECT $1 /* ? /* ? */ ? */, $2"},
		{`SELECT $$?$$, $text$'?'$text$, ?`, `SELECT $$?$$, $text$'?'$text$, $1`},
		{`SELECT E'escaped\'? question', ?`, `SELECT E'escaped\'? question', $1`},
		{`SELECT 'ordinary\', ?`, `SELECT 'ordinary\', $1`},
		{"SELECT 1", "SELECT 1"},
		{"SELECT 'unterminated ?", "SELECT 'unterminated ?"},
	} {
		if got := postgresBind(tc.query); got != tc.want {
			t.Errorf("bind(%q)=%q, want %q", tc.query, got, tc.want)
		}
	}
}
