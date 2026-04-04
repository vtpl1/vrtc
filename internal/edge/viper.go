package edge

import (
	"github.com/spf13/viper"
	"github.com/vtpl1/vrtc/internal/config"
)

// LoadConfig reads the JSON config file.
// Credentials are injected via explicit environment variables:
//
//	EDGE_MYSQL_CONFIG_USERNAME
//	EDGE_MYSQL_CONFIG_PASSWORD
func LoadConfig(cfgFile string) (*Config, error) {
	v := viper.New()
	// BindEnv maps specific config keys to their env vars.
	// AutomaticEnv cannot be used here because Viper uppercases the key
	// before applying the replacer, making prefix-stripping unreliable.
	_ = v.BindEnv("config.mysql_config.username", "EDGE_MYSQL_CONFIG_USERNAME")
	_ = v.BindEnv("config.mysql_config.password", "EDGE_MYSQL_CONFIG_PASSWORD")
	v.SetConfigFile(cfgFile)
	v.SetConfigType("json")

	err := v.ReadInConfig()
	if err != nil {
		return nil, err
	}

	var cfg Config

	err = v.Unmarshal(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func SaveConfig(cfgFile string) error {
	defaultCfg := Config{
		LiveRecordingConfig: LiveRecordingConfig{
			MediaServerID:             "",
			IsTestMode:                false,
			ClipDurationMins:          5,
			VMSIP:                     "127.0.0.1",
			SiteID:                    -1,
			MaxChannels:               -1,
			EnableMinorStreamGrabbing: true,
			EnableTCPServer:           false,
			EnableGRPCServer:          true,
			NASPaths:                  []string{""},
			EdgeEventManagerIP:        "127.0.0.1",
			PreMotionDurSecs:          10,
			PostMotionDurSecs:         10,
			MySQLConfig: MySQLConfig{
				Host:     "127.0.0.1",
				Port:     3306,
				Username: "", // set via EDGE_MYSQL_CONFIG_USERNAME env var
				Password: "", // set via EDGE_MYSQL_CONFIG_PASSWORD env var
			},
			EnableAlternateStreamGrabbing: false,

			ChannelSource:      "file",
			ScheduleSource:     "file",
			ChannelFilePath:    "",
			ScheduleFilePath:   "",
			RecordingIndexPath: "",
			APIListen:          ":8080",
			ChannelDB:          "edge",
			ScheduleDB:         "edge",
			MongoConfig: MongoConfig{
				URI:      "mongodb://localhost:27017",
				Database: "edge",
			},
			LogLevel: "debug",
		},
	}

	return config.SaveConfigJSON(cfgFile, map[string]any{
		"config": defaultCfg.LiveRecordingConfig,
	})
}
