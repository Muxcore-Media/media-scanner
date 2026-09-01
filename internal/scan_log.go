package internal

import (
	"context"
	"fmt"
	"time"
)

type scanTotals struct {
	found    int
	imported int
	skipped  int
}

func (m *Module) runLoggedScan(ctx context.Context, fn func(context.Context) (found, imported, skipped int, err error)) (scanTotals, error) {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return scanTotals{}, fmt.Errorf("not initialized")
	}
	if err := importPathCheckCtx(ctx); err != nil {
		return scanTotals{}, err
	}

	logID := fmt.Sprintf("scan_%d", time.Now().UnixNano())
	startedAt := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO scan_log (id, started_at, status) VALUES (?, ?, 'running')`, logID, startedAt)

	found, imported, skipped, err := fn(ctx)

	status := "completed"
	if err != nil {
		status = "failed"
	}
	completedAt := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.Exec(`UPDATE scan_log SET completed_at = ?, files_found = ?, files_imported = ?, files_skipped = ?, status = ? WHERE id = ?`,
		completedAt, found, imported, skipped, status, logID)

	return scanTotals{found: found, imported: imported, skipped: skipped}, err
}
