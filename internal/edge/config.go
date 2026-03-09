package edge

const AppName = "vrtc3"

//nolint:gosec
const DefaultSecretKey = "vtpl_e6a333d0f5616e2b0f28baebf0f41932"

type Config struct {
	Edge Edge `json:"edge" mapstructure:"edge" yaml:"edge"`
	API  API  `json:"api"  mapstructure:"api"  yaml:"api"`
}

type Edge struct {
	MySQLConnStr  string   `json:"mysql_conn_str"            mapstructure:"mysql_conn_str"  yaml:"mysql_conn_str"`            //nolint:tagliatelle
	MongoConnStr  string   `json:"mongo_conn_str"            mapstructure:"mongo_conn_str"  yaml:"mongo_conn_str"`            //nolint:tagliatelle
	VmsAddr       string   `json:"vms_addr"                  mapstructure:"vms_addr"        yaml:"vms_addr"`                  //nolint:tagliatelle
	SinkAddrs     []string `json:"sink_addrs"                mapstructure:"sink_addrs"      yaml:"sink_addrs"`                //nolint:tagliatelle
	SiteID        int      `json:"site_id"                   mapstructure:"site_id"         yaml:"site_id"`                   //nolint:tagliatelle
	IsPublicCloud bool     `json:"is_public_cloud,omitempty" mapstructure:"is_public_cloud" yaml:"is_public_cloud,omitempty"` //nolint:tagliatelle
	ChannelsCSV   string   `json:"channels_csv,omitempty"    mapstructure:"channels_csv"    yaml:"channels_csv,omitempty"`    //nolint:tagliatelle
	DontListen    bool     `json:"dont_listen,omitempty"     mapstructure:"dont_listen"     yaml:"dont_listen,omitempty"`     //nolint:tagliatelle
	StreamAddr    string   `json:"stream_addr,omitempty"     mapstructure:"stream_addr"     yaml:"stream_addr,omitempty"`     //nolint:tagliatelle
	StoragePath   string   `json:"storage_path,omitempty"    mapstructure:"storage_path"    yaml:"storage_path,omitempty"`    //nolint:tagliatelle
}

type API struct {
	Listen    int      `json:"listen"               mapstructure:"listen"     yaml:"listen"`
	StaticDir string   `json:"static_dir,omitempty" mapstructure:"static_dir" yaml:"static_dir,omitempty"` //nolint:tagliatelle
	Origins   []string `json:"origins,omitempty"    mapstructure:"origins"    yaml:"origins,omitempty"`
}
