package edge

import (
	"fmt"
	"net/url"
)

type Config struct {
	LiveRecordingConfig LiveRecordingConfig `json:"config" mapstructure:"config"`
}

type LiveRecordingConfig struct {
	SiteID                    int         `json:"site_id,omitempty"                      mapstructure:"site_id"`                      //nolint:tagliatelle
	MaxChannels               int         `json:"max_channels,omitempty"                 mapstructure:"max_channels"`                 //nolint:tagliatelle
	EnableMinorStreamGrabbing bool        `json:"enable_minor_stream_grabbing,omitempty" mapstructure:"enable_minor_stream_grabbing"` //nolint:tagliatelle
	EnableGRPCServer          bool        `json:"enable_grpc_server,omitempty"           mapstructure:"enable_grpc_server"`           //nolint:tagliatelle
	NASPaths                  []string    `json:"nas_paths"                              mapstructure:"nas_paths"`                    //nolint:tagliatelle
	EdgeEventManagerIP        string      `json:"edge_event_manager_ip,omitempty"        mapstructure:"edge_event_manager_ip"`        //nolint:tagliatelle
	IsTestMode                bool        `json:"is_test_mode,omitempty"                 mapstructure:"is_test_mode"`                 //nolint:tagliatelle
	PreMotionDurSecs          int         `json:"pre_motion_dur_secs,omitempty"          mapstructure:"pre_motion_dur_secs"`          //nolint:tagliatelle
	PostMotionDurSecs         int         `json:"post_motion_dur_secs,omitempty"         mapstructure:"post_motion_dur_secs"`         //nolint:tagliatelle
	MySQLConfig               MySQLConfig `json:"mysql_config"                           mapstructure:"mysql_config"`                 //nolint:tagliatelle
	ClipDurationMins          int         `json:"clip_duration_mins,omitempty"           mapstructure:"clip_duration_mins"`           //nolint:tagliatelle
	VMSIP                     string      `json:"vms_ip,omitempty"                       mapstructure:"vms_ip"`                       //nolint:tagliatelle

	// Channel / schedule / recording / API
	ChannelSource      string      `json:"channel_source,omitempty"       mapstructure:"channel_source"`       //nolint:tagliatelle
	ScheduleSource     string      `json:"schedule_source,omitempty"      mapstructure:"schedule_source"`      //nolint:tagliatelle
	ChannelFilePath    string      `json:"channel_file_path,omitempty"    mapstructure:"channel_file_path"`    //nolint:tagliatelle
	ScheduleFilePath   string      `json:"schedule_file_path,omitempty"   mapstructure:"schedule_file_path"`   //nolint:tagliatelle
	RecordingIndexPath string      `json:"recording_index_path,omitempty" mapstructure:"recording_index_path"` //nolint:tagliatelle
	APIListen          string      `json:"api_listen,omitempty"           mapstructure:"api_listen"`           //nolint:tagliatelle
	ChannelDB          string      `json:"channel_db,omitempty"           mapstructure:"channel_db"`           //nolint:tagliatelle
	ScheduleDB         string      `json:"schedule_db,omitempty"          mapstructure:"schedule_db"`          //nolint:tagliatelle
	MongoConfig        MongoConfig `json:"mongo_config"                   mapstructure:"mongo_config"`         //nolint:tagliatelle
	LogLevel           string      `json:"log_level,omitempty"            mapstructure:"log_level"`            //nolint:tagliatelle
	AuthToken          string      `json:"auth_token,omitempty"           mapstructure:"auth_token"`           //nolint:tagliatelle
	// AnalyticsDelay is how far behind live the analytics hub reads packets
	// (e.g. "5s"). This gives analytics time to arrive in the store before
	// the blocking merger processes each frame. Defaults to "5s".
	AnalyticsDelay string `json:"analytics_delay,omitempty" mapstructure:"analytics_delay"` //nolint:tagliatelle
	// AnalyticsMaxWait is the maximum time the blocking merger will wait for
	// analytics per video packet (e.g. "7s"). If analytics do not arrive
	// within this window, the packet passes through without them. Defaults to "7s".
	AnalyticsMaxWait string `json:"analytics_max_wait,omitempty" mapstructure:"analytics_max_wait"` //nolint:tagliatelle
}

// MongoConfig holds connection parameters for a MongoDB deployment.
type MongoConfig struct {
	URI      string `json:"uri"      mapstructure:"uri"`
	Database string `json:"database" mapstructure:"database"`
}

type MySQLConfig struct {
	Host     string `json:"host"     mapstructure:"host"`
	Port     int    `json:"port"     mapstructure:"port"`
	Username string `json:"username" mapstructure:"username"`
	Password string `json:"password" mapstructure:"password"`
}

func (m MySQLConfig) DSN(dbName string) string {
	u := &url.URL{
		User: url.UserPassword(m.Username, m.Password),
		Host: fmt.Sprintf("%s:%d", m.Host, m.Port),
	}

	path := "/"
	if dbName != "" {
		path += dbName
	}

	return fmt.Sprintf(
		"%s@tcp(%s)%s?parseTime=true&charset=utf8mb4&loc=Local",
		u.User.String(),
		u.Host,
		path,
	)
}
