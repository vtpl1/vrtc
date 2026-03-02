package cloud

import (
	"github.com/vtpl1/vrtc/internal/config"
)

func LoadConfig(cfgFile string) (*Config, error) {
	return config.LoadConfigYAML[Config](cfgFile)
}

func SaveConfig(cfgFile string) error {
	defaultCfg := Config{
		Cloud: Cloud{
			StreamAddr:     "http://127.0.0.1:20003",
			MongoConnStr:   "", // set via CLOUD_MONGO_CONN_STR env var
			StorageAPIAddr: "",
		},
		API: API{
			Listen:    8083,
			StaticDir: "ui",
			Origins:   nil,
		},
	}

	return config.SaveConfigYAML(cfgFile, map[string]any{
		"cloud": defaultCfg.Cloud,
		"api":   defaultCfg.API,
	})
}
