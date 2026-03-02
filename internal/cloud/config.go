package cloud

const AppName = "vrtc3-cloud"

//nolint:gosec
const DefaultSecretKey = "vtpl_e6a333d0f5616e2b0f28baebf0f41932"

type Config struct {
	Cloud Cloud `json:"cloud" mapstructure:"cloud" yaml:"cloud"`
	API   API   `json:"api"   mapstructure:"api"   yaml:"api"`
}

type Cloud struct {
	StreamAddr     string `json:"stream_addr,omitempty"      mapstructure:"stream_addr"      yaml:"stream_addr"`      //nolint:tagliatelle
	MongoConnStr   string `json:"mongo_conn_str,omitempty"   mapstructure:"mongo_conn_str"   yaml:"mongo_conn_str"`   //nolint:tagliatelle
	StorageAPIAddr string `json:"storage_api_addr,omitempty" mapstructure:"storage_api_addr" yaml:"storage_api_addr"` //nolint:tagliatelle
}

type API struct {
	Listen    int      `json:"listen"               mapstructure:"listen"     yaml:"listen"`
	StaticDir string   `json:"static_dir,omitempty" mapstructure:"static_dir" yaml:"static_dir,omitempty"` //nolint:tagliatelle
	Origins   []string `json:"origins,omitempty"    mapstructure:"origins"    yaml:"origins,omitempty"`
}
