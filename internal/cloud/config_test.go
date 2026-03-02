package cloud_test

import (
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/internal/cloud"
)

func TestSaveAndLoadConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfgFile := filepath.Join(t.TempDir(), "cloud.yaml")

	err := cloud.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg, err := cloud.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Cloud.StreamAddr != "http://127.0.0.1:20003" {
		t.Errorf("StreamAddr = %q, want default", cfg.Cloud.StreamAddr)
	}

	if cfg.API.Listen != 8083 {
		t.Errorf("API.Listen = %d, want 8083", cfg.API.Listen)
	}

	if cfg.Cloud.MongoConnStr != "" {
		t.Errorf("MongoConnStr = %q, want empty (should come from env)", cfg.Cloud.MongoConnStr)
	}
}

func TestLoadConfig_EnvOverridesSecrets(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "cloud.yaml")

	err := cloud.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	t.Setenv("CLOUD_MONGO_CONN_STR", "mongodb://testuser:testpass@10.0.0.1:27017/")

	cfg, err := cloud.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Cloud.MongoConnStr != "mongodb://testuser:testpass@10.0.0.1:27017/" { //nolint:gosec
		t.Errorf("MongoConnStr = %q, want env var value", cfg.Cloud.MongoConnStr)
	}

	// Non-secret defaults must survive the env override
	if cfg.Cloud.StreamAddr != "http://127.0.0.1:20003" {
		t.Errorf("StreamAddr = %q, want default", cfg.Cloud.StreamAddr)
	}
}
