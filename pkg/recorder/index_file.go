package recorder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// fileIndex implements RecordingIndex using an append-only newline-delimited
// JSON (NDJSON) file. Each event (start, complete, interrupted) is one line.
// QueryByChannel and SealInterrupted deduplicate by ID, keeping the
// highest-priority status for each segment ID.
type fileIndex struct {
	path string
	mu   sync.Mutex
}

// NewFileIndex returns a RecordingIndex backed by the NDJSON file at path.
// The file is created on first Insert if it does not exist.
func NewFileIndex(path string) RecordingIndex {
	return &fileIndex{path: path}
}

// statusPriority maps a status string to a numeric priority so that terminal
// statuses always win over transient ones when two entries share the same ID.
// deleted > complete > interrupted > recording (unknown = 0).
func statusPriority(status string) int {
	switch status {
	case StatusDeleted:
		return 3
	case StatusComplete:
		return 2
	case StatusInterrupted:
		return 1
	default: // StatusRecording or unknown
		return 0
	}
}

func (idx *fileIndex) Insert(_ context.Context, e RecordingEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("recorder index: marshal entry: %w", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	f, err := os.OpenFile(idx.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("recorder index: open %q: %w", idx.path, err)
	}

	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", line)
	if err != nil {
		return fmt.Errorf("recorder index: write entry: %w", err)
	}

	return nil
}

// SealInterrupted finds every segment ID whose last-known status is
// StatusRecording and appends a StatusInterrupted entry for it.
// Called once on startup before any new recordings begin.
func (idx *fileIndex) SealInterrupted(_ context.Context) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	best, err := idx.readBest()
	if err != nil {
		return err
	}

	f, err := os.OpenFile(idx.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("recorder index: open %q for seal: %w", idx.path, err)
	}

	defer f.Close()

	for _, e := range best {
		if e.Status != StatusRecording {
			continue
		}

		e.Status = StatusInterrupted

		line, merr := json.Marshal(e)
		if merr != nil {
			continue
		}

		if _, werr := fmt.Fprintf(f, "%s\n", line); werr != nil {
			return fmt.Errorf("recorder index: write interrupted entry: %w", werr)
		}
	}

	return nil
}

func (idx *fileIndex) QueryByChannel(
	_ context.Context,
	channelID string,
	from, to time.Time,
) ([]RecordingEntry, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	best, err := idx.readBest()
	if err != nil {
		return nil, err
	}

	var results []RecordingEntry

	for _, e := range best {
		if e.ChannelID != channelID {
			continue
		}

		// Exclude still-ongoing and deleted segments.
		if e.Status == StatusRecording || e.Status == StatusDeleted {
			continue
		}

		if !from.IsZero() && e.EndTime.Before(from) {
			continue
		}

		if !to.IsZero() && e.StartTime.After(to) {
			continue
		}

		results = append(results, e)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].StartTime.Before(results[j].StartTime)
	})

	return results, nil
}

func (idx *fileIndex) FirstAvailable(_ context.Context, channelID string) (RecordingEntry, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	best, err := idx.readBest()
	if err != nil {
		return RecordingEntry{}, err
	}

	var (
		found  bool
		hasAny bool // true if any non-deleted entry exists for this channel
		result RecordingEntry
	)

	for _, e := range best {
		if e.ChannelID != channelID {
			continue
		}

		if e.Status == StatusDeleted {
			continue
		}

		hasAny = true

		if e.Status == StatusRecording || e.Status == StatusCorrupted {
			continue
		}

		if !found || e.StartTime.Before(result.StartTime) {
			result = e
			found = true
		}
	}

	if !found {
		if hasAny {
			return RecordingEntry{}, ErrAllCorrupted
		}

		return RecordingEntry{}, ErrNoRecordings
	}

	return result, nil
}

func (idx *fileIndex) LastAvailable(_ context.Context, channelID string) (RecordingEntry, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	best, err := idx.readBest()
	if err != nil {
		return RecordingEntry{}, err
	}

	var (
		found  bool
		hasAny bool
		result RecordingEntry
	)

	for _, e := range best {
		if e.ChannelID != channelID {
			continue
		}

		if e.Status == StatusDeleted {
			continue
		}

		hasAny = true

		if e.Status == StatusRecording || e.Status == StatusCorrupted {
			continue
		}

		if !found || e.StartTime.After(result.StartTime) {
			result = e
			found = true
		}
	}

	if !found {
		if hasAny {
			return RecordingEntry{}, ErrAllCorrupted
		}

		return RecordingEntry{}, ErrNoRecordings
	}

	return result, nil
}

// Delete appends a StatusDeleted entry for id so that the segment is excluded
// from all future queries. The segment file must already have been removed by
// the caller.
func (idx *fileIndex) Delete(_ context.Context, id string) error {
	e := RecordingEntry{ID: id, Status: StatusDeleted}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("recorder index: marshal delete entry: %w", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	f, err := os.OpenFile(idx.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("recorder index: open %q: %w", idx.path, err)
	}

	defer f.Close()

	if _, err = fmt.Fprintf(f, "%s\n", line); err != nil {
		return fmt.Errorf("recorder index: write delete entry: %w", err)
	}

	return nil
}

func (idx *fileIndex) Close() error { return nil }

// readBest reads the NDJSON file and returns a map of segment ID → best entry,
// where "best" means highest statusPriority. Must be called with idx.mu held.
func (idx *fileIndex) readBest() (map[string]RecordingEntry, error) {
	f, err := os.Open(idx.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]RecordingEntry), nil
		}

		return nil, fmt.Errorf("recorder index: open %q: %w", idx.path, err)
	}

	defer f.Close()

	best := make(map[string]RecordingEntry)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var e RecordingEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}

		existing, ok := best[e.ID]
		if !ok || statusPriority(e.Status) > statusPriority(existing.Status) {
			best[e.ID] = e
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("recorder index: scan %q: %w", idx.path, err)
	}

	return best, nil
}
