package edge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/pkg/channel"
)

func TestRun_RequiresRecordingIndexPath(t *testing.T) {
	t.Parallel()

	err := Run(AppName, Config{})
	if !errors.Is(err, errIndexPathRequired) {
		t.Fatalf("expected errIndexPathRequired, got %v", err)
	}
}

func TestNewChannelProvider_FileRequiresPath(t *testing.T) {
	t.Parallel()

	_, err := newChannelProvider(context.Background(), LiveRecordingConfig{})
	if !errors.Is(err, errChannelFilePathRequired) {
		t.Fatalf("expected errChannelFilePathRequired, got %v", err)
	}
}

func TestNewScheduleProvider_FileRequiresPath(t *testing.T) {
	t.Parallel()

	_, err := newScheduleProvider(context.Background(), LiveRecordingConfig{})
	if !errors.Is(err, errScheduleFilePathRequired) {
		t.Fatalf("expected errScheduleFilePathRequired, got %v", err)
	}
}

func TestNewChannelProvider_FileBacked(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "channels.json")
	data, err := json.Marshal([]channel.Channel{
		{ID: "cam-1", Name: "Front Door", StreamURL: "rtsp://front/main"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write channels file: %v", err)
	}

	provider, err := newChannelProvider(context.Background(), LiveRecordingConfig{
		ChannelFilePath: path,
	})
	if err != nil {
		t.Fatalf("newChannelProvider: %v", err)
	}
	defer provider.Close()

	ch, err := provider.GetChannel(context.Background(), "cam-1")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}

	if ch.Name != "Front Door" {
		t.Fatalf("expected channel name Front Door, got %q", ch.Name)
	}
}

func TestNewScheduleProvider_FileBacked(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "schedules.json")
	payload := `[{"id":"sched-1","channelId":"cam-1","storagePath":"D:/recordings","segmentMinutes":5,"daysOfWeek":[1,2]}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write schedules file: %v", err)
	}

	provider, err := newScheduleProvider(context.Background(), LiveRecordingConfig{
		ScheduleFilePath: path,
	})
	if err != nil {
		t.Fatalf("newScheduleProvider: %v", err)
	}
	defer provider.Close()

	schedules, err := provider.ListSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}

	if len(schedules) != 1 || schedules[0].ChannelID != "cam-1" {
		t.Fatalf("unexpected schedules: %+v", schedules)
	}
}

func TestChannelAdapter_MapsIDs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "channels.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write initial channels file: %v", err)
	}

	provider := channel.NewFileProvider(path)
	if err := provider.SaveChannel(context.Background(), channel.Channel{ID: "cam-1", Name: "Front"}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}
	if err := provider.SaveChannel(context.Background(), channel.Channel{ID: "cam-2", Name: "Rear"}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}

	adapted, err := (channelAdapter{p: provider}).ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	if len(adapted) != 2 {
		t.Fatalf("expected 2 adapted channels, got %d", len(adapted))
	}

	if adapted[0].ID != "cam-1" || adapted[1].ID != "cam-2" {
		t.Fatalf("unexpected adapted channels: %+v", adapted)
	}
}
