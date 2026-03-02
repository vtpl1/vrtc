package liverecservice

import (
	"fmt"
	"net/url"
)

type Config struct {
	LiveRecordingConfig LiveRecordingConfig `json:"config" mapstructure:"config"`
}

type LiveRecordingConfig struct {
	MediaServerID             string      `json:"media_server_id,omitempty"              mapstructure:"media_server_id"`              //nolint:tagliatelle
	SiteID                    int         `json:"site_id,omitempty"                      mapstructure:"site_id"`                      //nolint:tagliatelle
	MaxChannels               int         `json:"max_channels,omitempty"                 mapstructure:"max_channels"`                 //nolint:tagliatelle
	EnableMinorStreamGrabbing bool        `json:"enable_minor_stream_grabbing,omitempty" mapstructure:"enable_minor_stream_grabbing"` //nolint:tagliatelle
	EnableTCPServer           bool        `json:"enable_tcp_server,omitempty"            mapstructure:"enable_tcp_server"`            //nolint:tagliatelle
	EnableGRPCServer          bool        `json:"enable_grpc_server,omitempty"           mapstructure:"enable_grpc_server"`           //nolint:tagliatelle
	DisableAVFSinkOverride    bool        `json:"disable_avf_sink_override,omitempty"    mapstructure:"disable_avf_sink_override"`    //nolint:tagliatelle
	NASPaths                  []string    `json:"nas_paths"                              mapstructure:"nas_paths"`                    //nolint:tagliatelle
	EdgeEventManagerIP        string      `json:"edge_event_manager_ip,omitempty"        mapstructure:"edge_event_manager_ip"`        //nolint:tagliatelle
	IsTestMode                bool        `json:"is_test_mode,omitempty"                 mapstructure:"is_test_mode"`                 //nolint:tagliatelle
	PreMotionDurSecs          int         `json:"pre_motion_dur_secs,omitempty"          mapstructure:"pre_motion_dur_secs"`          //nolint:tagliatelle
	PostMotionDurSecs         int         `json:"post_motion_dur_secs,omitempty"         mapstructure:"post_motion_dur_secs"`         //nolint:tagliatelle
	MySQLConfig               MySQLConfig `json:"mysql_config"                           mapstructure:"mysql_config"`                 //nolint:tagliatelle
	ClipDurationMins          int         `json:"clip_duration_mins,omitempty"           mapstructure:"clip_duration_mins"`           //nolint:tagliatelle
	VMSIP                     string      `json:"vms_ip,omitempty"                       mapstructure:"vms_ip"`                       //nolint:tagliatelle

	EnableAlternateStreamGrabbing bool `json:"enable_alternate_stream_grabbing,omitempty" mapstructure:"enable_alternate_stream_grabbing"` //nolint:tagliatelle
}

type MySQLConfig struct {
	Host     string `json:"host"     mapstructure:"host"`
	Port     int    `json:"port"     mapstructure:"port"`
	Username string `json:"username" mapstructure:"username"`
	Password string `json:"password" mapstructure:"password"` //nolint:gosec
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
