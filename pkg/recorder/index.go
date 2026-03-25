package recorder

import (
	"context"
	"time"
)

// RecordingEntry describes one completed recording segment stored on disk.
type RecordingEntry struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"` //nolint:tagliatelle
	StartTime time.Time `json:"start_time"` //nolint:tagliatelle
	EndTime   time.Time `json:"end_time"`   //nolint:tagliatelle
	FilePath  string    `json:"file_path"`  //nolint:tagliatelle
	SizeBytes int64     `json:"size_bytes"` //nolint:tagliatelle
}

// RecordingIndex persists completed segment metadata for later playback lookup.
// All methods must be safe for concurrent use.
type RecordingIndex interface {
	// Insert stores a completed RecordingEntry.
	Insert(ctx context.Context, e RecordingEntry) error

	// QueryByChannel returns all entries for channelID optionally filtered by
	// time range. A zero from or to means no lower/upper bound respectively.
	// Results are returned in ascending StartTime order.
	QueryByChannel(
		ctx context.Context,
		channelID string,
		from, to time.Time,
	) ([]RecordingEntry, error)

	// Close releases any held resources.
	Close() error
}
