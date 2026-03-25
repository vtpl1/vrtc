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
// JSON (NDJSON) file.  Each completed segment appends exactly one JSON line.
// QueryByChannel reads the whole file and filters in memory, which is
// appropriate for small deployments; replace with a database index for scale.
type fileIndex struct {
	path string
	mu   sync.Mutex
}

// NewFileIndex returns a RecordingIndex backed by the NDJSON file at path.
// The file is created if it does not exist.
func NewFileIndex(path string) RecordingIndex {
	return &fileIndex{path: path}
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

func (idx *fileIndex) QueryByChannel(
	_ context.Context,
	channelID string,
	from, to time.Time,
) ([]RecordingEntry, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	f, err := os.Open(idx.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("recorder index: open %q: %w", idx.path, err)
	}

	defer f.Close()

	var results []RecordingEntry

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var e RecordingEntry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip malformed lines rather than failing the whole query.
			continue
		}

		if e.ChannelID != channelID {
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

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("recorder index: scan %q: %w", idx.path, err)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].StartTime.Before(results[j].StartTime)
	})

	return results, nil
}

func (idx *fileIndex) Close() error { return nil }
