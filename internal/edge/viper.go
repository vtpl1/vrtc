package edge

import (
	"path/filepath"

	"github.com/vtpl1/vrtc/internal/config"
)

func LoadConfig(cfgFile string) (*Config, error) {
	return config.LoadConfigJSON[Config](cfgFile)
}

func SaveConfig(cfgFile string) error {
	// Derive default data paths relative to the config file's directory so that
	// the binary runs out of the box without any manual configuration.
	cfgDir := filepath.Dir(cfgFile)

	defaultCfg := Config{
		LiveRecordingConfig: LiveRecordingConfig{
			IsTestMode:                false,
			ClipDurationMins:          5,
			VMSIP:                     "127.0.0.1",
			SiteID:                    -1,
			MaxChannels:               -1,
			EnableMinorStreamGrabbing: true,
			EnableGRPCServer:          true,
			NASPaths:                  []string{""},
			EdgeEventManagerIP:        "127.0.0.1",
			PreMotionDurSecs:          10,
			PostMotionDurSecs:         10,
			ChannelFilePath:           filepath.Join(cfgDir, "channels.json"),
			ScheduleFilePath:          filepath.Join(cfgDir, "schedules.json"),
			RecordingIndexPath:        filepath.Join(cfgDir, "recordings"),
			APIListen:                 ":8080",
			LogLevel:                  "debug",
		},
	}

	return config.SaveConfigJSON(cfgFile, map[string]any{
		"config": defaultCfg.LiveRecordingConfig,
	})
}
