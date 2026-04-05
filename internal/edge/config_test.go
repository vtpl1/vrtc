package edge_test

import (
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/internal/edge"
)

func TestSaveAndLoadConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfgFile := filepath.Join(t.TempDir(), "edge.json")

	err := edge.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg, err := edge.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	lrc := cfg.LiveRecordingConfig

	if lrc.VMSIP != "127.0.0.1" {
		t.Errorf("VMSIP = %q, want default", lrc.VMSIP)
	}

	if lrc.ClipDurationMins != 5 {
		t.Errorf("ClipDurationMins = %d, want 5", lrc.ClipDurationMins)
	}

	if lrc.APIListen != ":8080" {
		t.Errorf("APIListen = %q, want %q", lrc.APIListen, ":8080")
	}

	// Paths that must have defaults so the binary runs out of the box.
	cfgDir := filepath.Dir(cfgFile)

	if lrc.ChannelFilePath != filepath.Join(cfgDir, "channels.json") {
		t.Errorf("ChannelFilePath = %q, want default", lrc.ChannelFilePath)
	}

	if lrc.ScheduleFilePath != filepath.Join(cfgDir, "schedules.json") {
		t.Errorf("ScheduleFilePath = %q, want default", lrc.ScheduleFilePath)
	}

	if lrc.RecordingIndexPath != filepath.Join(cfgDir, "recordings") {
		t.Errorf("RecordingIndexPath = %q, want default", lrc.RecordingIndexPath)
	}
}
