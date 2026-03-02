package liverecservice_test

import (
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/internal/liverecservice"
)

func TestSaveAndLoadConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfgFile := filepath.Join(t.TempDir(), "liverecservice.json")

	err := liverecservice.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg, err := liverecservice.LoadConfig(cfgFile)
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
}

func TestLoadConfig_EnvOverridesSecrets(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "liverecservice.json")

	err := liverecservice.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	t.Setenv("LIVE_REC_SERVICE_MYSQL_CONFIG_USERNAME", "dbuser")
	t.Setenv("LIVE_REC_SERVICE_MYSQL_CONFIG_PASSWORD", "s3cret")

	cfg, err := liverecservice.LoadConfig(cfgFile)
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

	mysqlCfg := liverecservice.MySQLConfig{
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
