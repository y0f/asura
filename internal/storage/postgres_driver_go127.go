//go:build postgres && go1.27

package storage

import "database/sql/driver"

// Go 1.27 selects RowsColumnScanner ahead of driver.Rows.Next. Preserve pgx's
// optimized scanning while applying the same privacy boundary to both paths.
func (rows postgresRows) NextRow() error {
	return postgresDriverError(rows.Rows.NextRow())
}

func (rows postgresRows) ScanColumn(ctx driver.ScanContext, index int, destination any) error {
	return postgresDriverError(rows.Rows.ScanColumn(ctx, index, destination))
}
