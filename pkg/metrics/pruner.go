package metrics

import (
	"context"
	"time"
)

// prune enforces retention and max-row limits on both tables.
func (s *Store) prune(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-s.retention).Format(time.RFC3339Nano)

	// Time-based retention.
	_, _ = s.db.ExecContext(ctx, "DELETE FROM samples WHERE ts < ?", cutoff)
	_, _ = s.db.ExecContext(ctx, "DELETE FROM snapshots WHERE ts < ?", cutoff)

	// Row-count cap on samples.
	if s.maxRows > 0 {
		var count int64

		row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM samples")
		if err := row.Scan(&count); err == nil && count > s.maxRows {
			keep := s.maxRows * 4 / 5 // keep 80%
			_, _ = s.db.ExecContext(
				ctx,
				"DELETE FROM samples WHERE id NOT IN (SELECT id FROM samples ORDER BY ts DESC LIMIT ?)",
				keep,
			)
		}
	}

	// Reclaim disk space.
	_, _ = s.db.ExecContext(ctx, "PRAGMA incremental_vacuum")
}
