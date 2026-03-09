package edge

import "github.com/vtpl1/vrtc/internal/config"

func LoadConfig(cfgFile string) (*Config, error) {
	return config.LoadConfigYAML[Config](cfgFile)
}

func SaveConfig(cfgFile string) error {
	defaultCfg := Config{
		Edge: Edge{
			VmsAddr:       "http://127.0.0.1:2500",
			MySQLConnStr:  "", // set via EDGE_MYSQL_CONN_STR env var
			MongoConnStr:  "", // set via EDGE_MONGO_CONN_STR env var
			SinkAddrs:     nil,
			SiteID:        0,
			IsPublicCloud: false,
			ChannelsCSV:   "",
			DontListen:    false,
			StreamAddr:    "",
			StoragePath:   "./test_data",
		},
		API: API{
			Listen:    8083,
			StaticDir: "",
			Origins:   nil,
		},
	}

	return config.SaveConfigYAML(cfgFile, map[string]any{
		"edge": defaultCfg.Edge,
		"api":  defaultCfg.API,
	})
}
