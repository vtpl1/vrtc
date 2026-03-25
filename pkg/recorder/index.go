package recorder

import (
	"context"
	"time"
)

// Recording status values stored in RecordingEntry.Status.
const (
	// StatusRecording is written when a segment starts. If the process is
	// killed before the segment closes, this status persists in the index until
	// SealInterrupted is called on the next startup.
	StatusRecording = "recording"

	// StatusComplete is written when a segment closes normally.
	StatusComplete = "complete"

	// StatusInterrupted is written by SealInterrupted for segments that were
	// still in StatusRecording when the process restarted.
	StatusInterrupted = "interrupted"

	// StatusDeleted is written by the retention enforcer after the segment file
	// has been removed from disk. Deleted entries are excluded from all queries.
	StatusDeleted = "deleted"

	// StatusCorrupted is written when a segment is detected as corrupt (e.g.
	// failed integrity check). Corrupted entries are excluded from playback
	// queries the same way deleted entries are.
	StatusCorrupted = "corrupted"
)

// RecordingEntry describes one recording segment stored on disk.
type RecordingEntry struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"` //nolint:tagliatelle
	StartTime  time.Time `json:"start_time"` //nolint:tagliatelle
	EndTime    time.Time `json:"end_time"`   //nolint:tagliatelle
	FilePath   string    `json:"file_path"`  //nolint:tagliatelle
	SizeBytes  int64     `json:"size_bytes"` //nolint:tagliatelle
	Status     string    `json:"status"`
	HasMotion  bool      `json:"has_motion"`  //nolint:tagliatelle
	HasObjects bool      `json:"has_objects"` //nolint:tagliatelle
}

// SeekEntry represents a keyframe position within a recording segment,
// mapping a DTS timestamp (in milliseconds) to a byte offset in the file.
type SeekEntry struct {
	DTSMS      int64
	ByteOffset int64
}

// RecordingIndex persists segment metadata for later playback lookup.
// All methods must be safe for concurrent use.
type RecordingIndex interface {
	// Insert stores a RecordingEntry (any status).
	Insert(ctx context.Context, e RecordingEntry) error

	// QueryByChannel returns entries for channelID optionally filtered by time
	// range. A zero from or to means no lower/upper bound. StatusRecording
	// entries are excluded; StatusComplete and StatusInterrupted are included.
	// Results are returned in ascending StartTime order.
	QueryByChannel(
		ctx context.Context,
		channelID string,
		from, to time.Time,
	) ([]RecordingEntry, error)

	// Delete marks a segment as deleted in the index. The caller is responsible
	// for removing the file from disk before calling Delete.
	Delete(ctx context.Context, id string) error

	// SealInterrupted finds every entry whose last-known status is
	// StatusRecording and appends a StatusInterrupted entry for it. Call once
	// on startup before starting any new recordings.
	SealInterrupted(ctx context.Context) error

	// Close releases any held resources.
	Close() error
}
