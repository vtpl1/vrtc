package recorder

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNoRecordings is returned by FirstAvailable and LastAvailable when no
	// playable recordings exist for the requested channel.
	ErrNoRecordings = errors.New("no playable recordings found")

	// ErrAllCorrupted is returned by FirstAvailable and LastAvailable when
	// recordings exist for the channel but all are corrupted or deleted.
	ErrAllCorrupted = errors.New("all recordings are corrupted")
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
	ChannelID  string    `json:"channelId"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	FilePath   string    `json:"filePath"`
	SizeBytes  int64     `json:"sizeBytes"`
	Status     string    `json:"status"`
	HasMotion  bool      `json:"hasMotion"`
	HasObjects bool      `json:"hasObjects"`
	HasEvents  bool      `json:"hasEvents"`
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

	// FirstAvailable returns the earliest playable recording for the channel.
	// Only StatusComplete and StatusInterrupted entries are considered.
	// Returns ErrNoRecordings if no playable recordings exist.
	FirstAvailable(ctx context.Context, channelID string) (RecordingEntry, error)

	// LastAvailable returns the latest playable recording for the channel.
	// Only StatusComplete and StatusInterrupted entries are considered.
	// Returns ErrNoRecordings if no playable recordings exist.
	LastAvailable(ctx context.Context, channelID string) (RecordingEntry, error)

	// Delete marks a segment as deleted in the index. The caller should call
	// Delete before removing the file from disk, so that concurrent readers
	// see the entry as deleted before the file disappears.
	Delete(ctx context.Context, id string) error

	// QueryAllChannels returns entries across every known channel, optionally
	// filtered by time range. A zero from or to means no lower/upper bound.
	// Used for cross-channel metrics aggregation.
	QueryAllChannels(ctx context.Context, from, to time.Time) ([]RecordingEntry, error)

	// SealInterrupted finds every entry whose last-known status is
	// StatusRecording and appends a StatusInterrupted entry for it. Call once
	// on startup before starting any new recordings.
	SealInterrupted(ctx context.Context) error

	// Close releases any held resources.
	Close() error
}
