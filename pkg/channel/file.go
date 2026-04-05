package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// fileProvider implements ChannelWriter by reading/writing a JSON file.
// The file must contain a JSON array of Channel objects.
//
// Example file format:
//
//	[
//	  {"id":"cam-1","name":"Front Door","streamUrl":"rtsp://10.0.0.1/main","siteId":1},
//	  {"id":"cam-2","name":"Back Yard", "streamUrl":"rtsp://10.0.0.2/main","siteId":1}
//	]
type fileProvider struct {
	path string
	mu   sync.Mutex
}

// NewFileProvider returns a ChannelWriter backed by the JSON file at path.
// The file is re-read on every GetChannel / ListChannels call so changes are
// picked up automatically without restarting the process.
func NewFileProvider(path string) ChannelWriter {
	return &fileProvider{path: path}
}

func (p *fileProvider) GetChannel(_ context.Context, id string) (Channel, error) {
	channels, err := p.load()
	if err != nil {
		return Channel{}, err
	}

	for _, ch := range channels {
		if ch.ID == id {
			return ch, nil
		}
	}

	return Channel{}, fmt.Errorf("%w: %s", ErrChannelNotFound, id)
}

func (p *fileProvider) ListChannels(_ context.Context) ([]Channel, error) {
	return p.load()
}

func (p *fileProvider) SaveChannel(_ context.Context, ch Channel) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	channels, err := p.load()
	if err != nil {
		// If the file doesn't exist yet, start with an empty list.
		if os.IsNotExist(err) {
			channels = nil
		} else {
			return err
		}
	}

	found := false

	for i, existing := range channels {
		if existing.ID == ch.ID {
			channels[i] = ch
			found = true

			break
		}
	}

	if !found {
		channels = append(channels, ch)
	}

	return p.save(channels)
}

func (p *fileProvider) DeleteChannel(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	channels, err := p.load()
	if err != nil {
		return err
	}

	found := false

	for i, ch := range channels {
		if ch.ID == id {
			channels = append(channels[:i], channels[i+1:]...)
			found = true

			break
		}
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrChannelNotFound, id)
	}

	return p.save(channels)
}

func (p *fileProvider) Close() error { return nil }

func (p *fileProvider) load() ([]Channel, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return nil, fmt.Errorf("channel file provider: read %q: %w", p.path, err)
	}

	var channels []Channel
	if err := json.Unmarshal(data, &channels); err != nil {
		return nil, fmt.Errorf("channel file provider: parse %q: %w", p.path, err)
	}

	return channels, nil
}

func (p *fileProvider) save(channels []Channel) error {
	data, err := json.MarshalIndent(
		channels,
		"",
		"  ",
	)
	if err != nil {
		return fmt.Errorf("channel file provider: marshal: %w", err)
	}

	//nolint:gosec // channel file is non-sensitive config, 0644 is appropriate
	if err := os.WriteFile(p.path, data, 0o644); err != nil {
		return fmt.Errorf("channel file provider: write %q: %w", p.path, err)
	}

	return nil
}
