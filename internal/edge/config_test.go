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

	if lrc.MySQLConfig.Username != "" {
		t.Errorf("Username = %q, want empty (should come from env)", lrc.MySQLConfig.Username)
	}

	if lrc.MySQLConfig.Password != "" {
		t.Errorf("Password = %q, want empty (should come from env)", lrc.MySQLConfig.Password)
	}

	if lrc.ChannelSource != "file" {
		t.Errorf("ChannelSource = %q, want %q", lrc.ChannelSource, "file")
	}

	if lrc.ScheduleSource != "file" {
		t.Errorf("ScheduleSource = %q, want %q", lrc.ScheduleSource, "file")
	}

	if lrc.APIListen != ":8080" {
		t.Errorf("APIListen = %q, want %q", lrc.APIListen, ":8080")
	}
}

func TestLoadConfig_EnvOverridesSecrets(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "edge.json")

	err := edge.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	t.Setenv("EDGE_MYSQL_CONFIG_USERNAME", "dbuser")
	t.Setenv("EDGE_MYSQL_CONFIG_PASSWORD", "s3cret")

	cfg, err := edge.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	lrc := cfg.LiveRecordingConfig

	if lrc.MySQLConfig.Username != "dbuser" {
		t.Errorf("Username = %q, want env var value", lrc.MySQLConfig.Username)
	}

	if lrc.MySQLConfig.Password != "s3cret" {
		t.Errorf("Password = %q, want env var value", lrc.MySQLConfig.Password)
	}

	// Non-secret defaults must survive the env override
	if lrc.VMSIP != "127.0.0.1" {
		t.Errorf("VMSIP = %q, want default", lrc.VMSIP)
	}

	if lrc.ClipDurationMins != 5 {
		t.Errorf("ClipDurationMins = %d, want 5", lrc.ClipDurationMins)
	}
}

func TestMySQLConfig_DSN(t *testing.T) {
	t.Parallel()

	mysqlCfg := edge.MySQLConfig{
		Host:     "10.0.0.1",
		Port:     3306,
		Username: "dbuser",
		Password: "s3cret",
	}

	got := mysqlCfg.DSN("mydb")
	want := "dbuser:s3cret@tcp(10.0.0.1:3306)/mydb?parseTime=true&charset=utf8mb4&loc=Local"

	if got != want {
		t.Errorf("DSN() = %q, want %q", got, want)
	}
}
