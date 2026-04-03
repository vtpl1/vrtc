package recorder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

const (
	// maxIdleDBAge is the duration after which an idle per-channel database
	// connection is eligible for eviction to bound open file handles.
	maxIdleDBAge = 10 * time.Minute

	// evictionThreshold is the number of open databases above which lazy
	// eviction is triggered on each getDB call.
	evictionThreshold = 100
)

// sqliteIndex implements RecordingIndex using per-channel SQLite databases.
type sqliteIndex struct {
	baseDir    string
	mu         sync.RWMutex
	dbs        map[string]*sql.DB
	lastAccess map[string]time.Time // tracks last access time per channel for LRU eviction
}

// NewSQLiteIndex returns a RecordingIndex backed by per-channel SQLite databases.
func NewSQLiteIndex(baseDir string) RecordingIndex {
	return &sqliteIndex{
		baseDir:    baseDir,
		dbs:        make(map[string]*sql.DB),
		lastAccess: make(map[string]time.Time),
	}
}

func (idx *sqliteIndex) Insert(ctx context.Context, e RecordingEntry) error {
	db, err := idx.getDB(ctx, e.ChannelID)
	if err != nil {
		return err
	}

	const query = `INSERT OR REPLACE INTO recordings
		(id, start_time, end_time, file_path, size_bytes, status, has_motion, has_objects)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = db.ExecContext(ctx, query,
		e.ID,
		e.StartTime.UTC().Format(time.RFC3339Nano),
		e.EndTime.UTC().Format(time.RFC3339Nano),
		e.FilePath,
		e.SizeBytes,
		e.Status,
		boolToInt(e.HasMotion),
		boolToInt(e.HasObjects),
	)
	if err != nil {
		return fmt.Errorf("recorder sqlite: insert %q: %w", e.ID, err)
	}

	return nil
}

func (idx *sqliteIndex) QueryByChannel(
	ctx context.Context,
	channelID string,
	from, to time.Time,
) ([]RecordingEntry, error) {
	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return nil, err
	}

	query := `SELECT id, start_time, end_time, file_path, size_bytes, status, has_motion, has_objects
		FROM recordings
		WHERE status NOT IN ('recording', 'deleted')`

	var args []any

	if !from.IsZero() {
		query += " AND end_time >= ?"

		args = append(args, from.UTC().Format(time.RFC3339Nano))
	}

	if !to.IsZero() {
		query += " AND start_time <= ?"

		args = append(args, to.UTC().Format(time.RFC3339Nano))
	}

	query += " ORDER BY start_time ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recorder sqlite: query channel %q: %w", channelID, err)
	}
	defer rows.Close()

	var results []RecordingEntry

	for rows.Next() {
		e, scanErr := scanRecordingRow(rows, channelID)
		if scanErr != nil {
			return nil, scanErr
		}

		results = append(results, e)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("recorder sqlite: rows iteration: %w", err)
	}

	return results, nil
}

func scanRecordingRow(rows *sql.Rows, channelID string) (RecordingEntry, error) {
	var (
		e                       RecordingEntry
		startStr, endStr        string
		hasMotionInt, hasObjInt int
	)

	if err := rows.Scan(
		&e.ID, &startStr, &endStr, &e.FilePath, &e.SizeBytes, &e.Status,
		&hasMotionInt, &hasObjInt,
	); err != nil {
		return RecordingEntry{}, fmt.Errorf("recorder sqlite: scan row: %w", err)
	}

	e.ChannelID = channelID
	e.StartTime, _ = time.Parse(time.RFC3339Nano, startStr)
	e.EndTime, _ = time.Parse(time.RFC3339Nano, endStr)
	e.HasMotion = hasMotionInt != 0
	e.HasObjects = hasObjInt != 0

	return e, nil
}

func (idx *sqliteIndex) FirstAvailable(
	ctx context.Context,
	channelID string,
) (RecordingEntry, error) {
	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return RecordingEntry{}, err
	}

	const query = `SELECT id, start_time, end_time, file_path, size_bytes, status, has_motion, has_objects
		FROM recordings
		WHERE status NOT IN ('recording', 'deleted', 'corrupted')
		ORDER BY start_time ASC
		LIMIT 1`

	row := db.QueryRowContext(ctx, query)

	e, err := scanSingleRow(row, channelID)
	if err != nil {
		return RecordingEntry{}, fmt.Errorf(
			"recorder sqlite: first available for %q: %w",
			channelID,
			err,
		)
	}

	return e, nil
}

func (idx *sqliteIndex) LastAvailable(
	ctx context.Context,
	channelID string,
) (RecordingEntry, error) {
	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return RecordingEntry{}, err
	}

	const query = `SELECT id, start_time, end_time, file_path, size_bytes, status, has_motion, has_objects
		FROM recordings
		WHERE status NOT IN ('recording', 'deleted', 'corrupted')
		ORDER BY start_time DESC
		LIMIT 1`

	row := db.QueryRowContext(ctx, query)

	e, err := scanSingleRow(row, channelID)
	if err != nil {
		return RecordingEntry{}, fmt.Errorf(
			"recorder sqlite: last available for %q: %w",
			channelID,
			err,
		)
	}

	return e, nil
}

// scanSingleRow scans a single recording row from a *sql.Row. Returns
// ErrNoRecordings if no row is found.
func scanSingleRow(row *sql.Row, channelID string) (RecordingEntry, error) {
	var (
		e                       RecordingEntry
		startStr, endStr        string
		hasMotionInt, hasObjInt int
	)

	if err := row.Scan(
		&e.ID, &startStr, &endStr, &e.FilePath, &e.SizeBytes, &e.Status,
		&hasMotionInt, &hasObjInt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RecordingEntry{}, ErrNoRecordings
		}

		return RecordingEntry{}, fmt.Errorf("scan row: %w", err)
	}

	e.ChannelID = channelID
	e.StartTime, _ = time.Parse(time.RFC3339Nano, startStr)
	e.EndTime, _ = time.Parse(time.RFC3339Nano, endStr)
	e.HasMotion = hasMotionInt != 0
	e.HasObjects = hasObjInt != 0

	return e, nil
}

func (idx *sqliteIndex) Delete(ctx context.Context, id string) error {
	idx.mu.RLock()
	snapshot := make(map[string]*sql.DB, len(idx.dbs))
	maps.Copy(snapshot, idx.dbs)
	idx.mu.RUnlock()

	const query = `UPDATE recordings SET status = ? WHERE id = ?`

	for _, db := range snapshot {
		res, err := db.ExecContext(ctx, query, StatusDeleted, id)
		if err != nil {
			return fmt.Errorf("recorder sqlite: delete %q: %w", id, err)
		}

		n, _ := res.RowsAffected()
		if n > 0 {
			return nil
		}
	}

	return nil
}

func (idx *sqliteIndex) SealInterrupted(ctx context.Context) error {
	idx.mu.RLock()
	snapshot := make(map[string]*sql.DB, len(idx.dbs))
	maps.Copy(snapshot, idx.dbs)
	idx.mu.RUnlock()

	const query = `UPDATE recordings SET status = ? WHERE status = ?`

	for ch, db := range snapshot {
		if _, err := db.ExecContext(ctx, query, StatusInterrupted, StatusRecording); err != nil {
			return fmt.Errorf("recorder sqlite: seal interrupted for channel %q: %w", ch, err)
		}
	}

	return nil
}

func (idx *sqliteIndex) Close() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	var firstErr error

	for ch, db := range idx.dbs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("recorder sqlite: close %q: %w", ch, err)
		}

		delete(idx.dbs, ch)
	}

	return firstErr
}

// InsertSeekEntries bulk-inserts seek entries for a recording segment.
func (idx *sqliteIndex) InsertSeekEntries(
	ctx context.Context,
	channelID, recordingID string,
	entries []SeekEntry,
) error {
	if len(entries) == 0 {
		return nil
	}

	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recorder sqlite: begin tx for seek entries: %w", err)
	}

	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO seek_index (recording_id, dts_ms, byte_offset) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("recorder sqlite: prepare seek insert: %w", err)
	}

	defer stmt.Close()

	for _, se := range entries {
		if _, err = stmt.ExecContext(ctx, recordingID, se.DTSMS, se.ByteOffset); err != nil {
			return fmt.Errorf("recorder sqlite: insert seek entry: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("recorder sqlite: commit seek entries: %w", err)
	}

	return nil
}

// SeekInSegment returns the byte offset of the keyframe at or just before
// dtsMS within the given recording segment.
func (idx *sqliteIndex) SeekInSegment(
	ctx context.Context,
	channelID, recordingID string,
	dtsMS int64,
) (int64, error) {
	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return 0, err
	}

	const query = `SELECT byte_offset FROM seek_index
		WHERE recording_id = ? AND dts_ms <= ?
		ORDER BY dts_ms DESC LIMIT 1`

	var offset int64

	err = db.QueryRowContext(ctx, query, recordingID, dtsMS).Scan(&offset)
	if err != nil {
		return 0, fmt.Errorf("recorder sqlite: seek in %q at %d ms: %w", recordingID, dtsMS, err)
	}

	return offset, nil
}

// Vacuum runs VACUUM on the per-channel SQLite database to reclaim space
// after bulk deletes (e.g. retention enforcement). The caller should rate-
// limit invocations (e.g. once per day per channel) and avoid peak load.
func (idx *sqliteIndex) Vacuum(ctx context.Context, channelID string) error {
	db, err := idx.getDB(ctx, channelID)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "VACUUM")
	if err != nil {
		return fmt.Errorf("recorder sqlite: vacuum %q: %w", channelID, err)
	}

	return nil
}

func (*sqliteIndex) initSchema(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	}

	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("recorder sqlite: %s: %w", p, err)
		}
	}

	ddl := `
CREATE TABLE IF NOT EXISTS recordings (
    id         TEXT PRIMARY KEY,
    start_time TEXT NOT NULL,
    end_time   TEXT DEFAULT '',
    file_path  TEXT NOT NULL,
    size_bytes INTEGER DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'recording',
    has_motion  INTEGER DEFAULT 0,
    has_objects INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_start        ON recordings(start_time);
CREATE INDEX IF NOT EXISTS idx_status       ON recordings(status);
CREATE INDEX IF NOT EXISTS idx_status_start ON recordings(status, start_time);
CREATE INDEX IF NOT EXISTS idx_end          ON recordings(end_time);

CREATE TABLE IF NOT EXISTS seek_index (
    recording_id TEXT    NOT NULL,
    dts_ms       INTEGER NOT NULL,
    byte_offset  INTEGER NOT NULL,
    PRIMARY KEY (recording_id, dts_ms)
);`

	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("recorder sqlite: create schema: %w", err)
	}

	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// getDB returns the *sql.DB for the given channelID, opening it lazily.
// It also triggers LRU eviction of idle databases when the open count
// exceeds evictionThreshold.
func (idx *sqliteIndex) getDB(ctx context.Context, channelID string) (*sql.DB, error) {
	idx.mu.RLock()
	db, ok := idx.dbs[channelID]
	idx.mu.RUnlock()

	if ok {
		idx.mu.Lock()
		idx.lastAccess[channelID] = time.Now()
		idx.mu.Unlock()

		return db, nil
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if db, ok = idx.dbs[channelID]; ok {
		idx.lastAccess[channelID] = time.Now()

		return db, nil
	}

	// Evict idle databases before opening a new one.
	if len(idx.dbs) >= evictionThreshold {
		idx.evictIdleLocked()
	}

	dir := filepath.Join(idx.baseDir, channelID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("recorder sqlite: mkdir %q: %w", dir, err)
	}

	dsn := filepath.Join(dir, "index.db")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("recorder sqlite: open %q: %w", dsn, err)
	}

	if err = idx.initSchema(ctx, db); err != nil {
		db.Close()

		return nil, err
	}

	// SQLite serialises writes; 2 conns is enough. This bounds file handle
	// usage when hundreds of per-channel databases are open concurrently.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)

	idx.dbs[channelID] = db
	idx.lastAccess[channelID] = time.Now()

	return db, nil
}

// evictIdleLocked closes databases that have not been accessed for
// maxIdleDBAge. Must be called with idx.mu held for writing.
func (idx *sqliteIndex) evictIdleLocked() {
	cutoff := time.Now().Add(-maxIdleDBAge)

	for ch, lastAt := range idx.lastAccess {
		if lastAt.Before(cutoff) {
			if db, ok := idx.dbs[ch]; ok {
				_ = db.Close()

				delete(idx.dbs, ch)
			}

			delete(idx.lastAccess, ch)
		}
	}
}
