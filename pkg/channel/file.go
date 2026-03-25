package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// fileProvider implements ChannelProvider by reading a JSON file on every call.
// The file must contain a JSON array of Channel objects.
//
// Example file format:
//
//	[
//	  {"id":"cam-1","name":"Front Door","stream_url":"rtsp://10.0.0.1/main","site_id":1},
//	  {"id":"cam-2","name":"Back Yard", "stream_url":"rtsp://10.0.0.2/main","site_id":1}
//	]
type fileProvider struct {
	path string
}

// NewFileProvider returns a ChannelProvider backed by the JSON file at path.
// The file is re-read on every GetChannel / ListChannels call so changes are
// picked up automatically without restarting the process.
func NewFileProvider(path string) ChannelProvider {
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
