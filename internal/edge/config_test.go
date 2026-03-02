package edge_test

import (
	"path/filepath"
	"testing"

	"github.com/vtpl1/vrtc/internal/edge"
)

func TestSaveAndLoadConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfgFile := filepath.Join(t.TempDir(), "edge.yaml")

	err := edge.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg, err := edge.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Edge.VmsAddr != "http://127.0.0.1:2500" {
		t.Errorf("VmsAddr = %q, want default", cfg.Edge.VmsAddr)
	}

	if cfg.API.Listen != 8083 {
		t.Errorf("API.Listen = %d, want 8083", cfg.API.Listen)
	}

	if cfg.Edge.MySQLConnStr != "" {
		t.Errorf("MySQLConnStr = %q, want empty (should come from env)", cfg.Edge.MySQLConnStr)
	}

	if cfg.Edge.MongoConnStr != "" {
		t.Errorf("MongoConnStr = %q, want empty (should come from env)", cfg.Edge.MongoConnStr)
	}
}

func TestLoadConfig_EnvOverridesSecrets(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "edge.yaml")

	err := edge.SaveConfig(cfgFile)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	t.Setenv("EDGE_MYSQL_CONN_STR", "testuser:testpass@tcp(10.0.0.1:3306)/testdb")
	t.Setenv("EDGE_MONGO_CONN_STR", "mongodb://testuser:testpass@10.0.0.1:27017/")

	cfg, err := edge.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Edge.MySQLConnStr != "testuser:testpass@tcp(10.0.0.1:3306)/testdb" {
		t.Errorf("MySQLConnStr = %q, want env var value", cfg.Edge.MySQLConnStr)
	}

	if cfg.Edge.MongoConnStr != "mongodb://testuser:testpass@10.0.0.1:27017/" { //nolint:gosec
		t.Errorf("MongoConnStr = %q, want env var value", cfg.Edge.MongoConnStr)
	}

	// Non-secret defaults must survive the env override
	if cfg.Edge.VmsAddr != "http://127.0.0.1:2500" {
		t.Errorf("VmsAddr = %q, want default", cfg.Edge.VmsAddr)
	}
}
