package channel_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/pkg/channel"
)

func writeTempChannelFile(t *testing.T, channels []channel.Channel) string {
	t.Helper()

	data, err := json.Marshal(channels)
	if err != nil {
		t.Fatalf("marshal channels: %v", err)
	}

	path := filepath.Join(t.TempDir(), "channels.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	return path
}

func TestFileProvider_ListChannels(t *testing.T) {
	t.Parallel()

	want := []channel.Channel{
		{ID: "cam-1", Name: "Front Door", StreamURL: "rtsp://10.0.0.1/main", SiteID: 1},
		{ID: "cam-2", Name: "Back Yard", StreamURL: "rtsp://10.0.0.2/main", SiteID: 1},
	}

	path := writeTempChannelFile(t, want)

	p := channel.NewFileProvider(path)
	defer p.Close()

	got, err := p.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d channels, want %d", len(got), len(want))
	}

	for i, ch := range got {
		if ch.ID != want[i].ID || ch.StreamURL != want[i].StreamURL {
			t.Errorf("channel[%d] = %+v, want %+v", i, ch, want[i])
		}
	}
}

func TestFileProvider_GetChannel_Found(t *testing.T) {
	t.Parallel()

	channels := []channel.Channel{
		{ID: "cam-1", Name: "Front Door", StreamURL: "rtsp://10.0.0.1/main", SiteID: 1},
	}

	p := channel.NewFileProvider(writeTempChannelFile(t, channels))
	defer p.Close()

	got, err := p.GetChannel(context.Background(), "cam-1")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}

	if got.StreamURL != "rtsp://10.0.0.1/main" {
		t.Errorf("StreamURL = %q, want rtsp://10.0.0.1/main", got.StreamURL)
	}
}

func TestFileProvider_GetChannel_NotFound(t *testing.T) {
	t.Parallel()

	p := channel.NewFileProvider(writeTempChannelFile(t, []channel.Channel{}))
	defer p.Close()

	_, err := p.GetChannel(context.Background(), "no-such-channel")
	if !errors.Is(err, channel.ErrChannelNotFound) {
		t.Errorf("got %v, want ErrChannelNotFound", err)
	}
}

func TestFileProvider_MissingFile(t *testing.T) {
	t.Parallel()

	p := channel.NewFileProvider("/nonexistent/channels.json")
	defer p.Close()

	_, err := p.ListChannels(context.Background())
	if err == nil {
		t.Error("expected an error for missing file, got nil")
	}
}
