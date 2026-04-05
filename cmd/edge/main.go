package main

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/vtpl1/vrtc/internal/edge"
	"github.com/vtpl1/vrtc/pkg/appinfo"
	"github.com/vtpl1/vrtc/pkg/configpath"
	"github.com/vtpl1/vrtc/pkg/logger"
)

func newRootCmd() *cobra.Command {
	var cfgGlobal edge.Config

	return &cobra.Command{
		Use:     edge.AppName,
		Short:   edge.AppName,
		Version: appinfo.GetVersion(),
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			cfgFile := configpath.GetJSONConfigFilePath(edge.AppName)

			cfg, err := edge.LoadConfig(cfgFile)
			if err != nil {
				log.Warn().Err(err).Msg("Config not found. Creating default config")

				err := edge.SaveConfig(cfgFile)
				if err != nil {
					return err
				}

				// Bootstrap empty seed files so file-backed providers work
				// out of the box.
				bootstrapSeedFiles(cfgFile)

				cfg, err = edge.LoadConfig(cfgFile)
				if err != nil {
					return err
				}
			}

			cfgGlobal = *cfg

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			logLevel := cfgGlobal.LiveRecordingConfig.LogLevel
			if logLevel == "" {
				logLevel = "debug"
			}

			logFile := configpath.GetLogFilePath(edge.AppName)

			closeLog, err := logger.InitLogger(logFile, logLevel)
			if err != nil {
				return err
			}

			defer closeLog()

			log.Info().
				Str("appName", edge.AppName).
				Str("version", appinfo.GetVersion()).
				Str("logFile", logFile).
				Str("logLevel", logLevel).
				Msg("starting")

			return edge.Run(edge.AppName, cfgGlobal)
		},
	}
}

// bootstrapSeedFiles creates empty JSON array files for channels and schedules
// so the file-backed providers work immediately after generating a default config.
func bootstrapSeedFiles(cfgFile string) {
	cfgDir := filepath.Dir(cfgFile)

	for _, name := range []string{"channels.json", "schedules.json"} {
		p := filepath.Join(cfgDir, name)
		if _, err := os.Stat(p); err == nil {
			continue // already exists
		}

		if err := os.WriteFile(p, []byte("[]"), 0o644); err != nil { //nolint:gosec
			log.Warn().Err(err).Str("path", p).Msg("failed to create seed file")
		}
	}
}

func main() {
	// Load .env into the process environment before Viper reads it.
	// Non-fatal: silently ignored when the file does not exist.
	_ = godotenv.Load()

	if err := newRootCmd().Execute(); err != nil {
		log.Error().Err(err).Msg("command failed")
		os.Exit(1)
	}
}
