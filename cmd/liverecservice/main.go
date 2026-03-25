package main

import (
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/vtpl1/vrtc/internal/liverecservice"
	"github.com/vtpl1/vrtc/pkg/appinfo"
	"github.com/vtpl1/vrtc/pkg/configpath"
	"github.com/vtpl1/vrtc/pkg/logger"
)

func main() {
	// Load .env into the process environment before Viper reads it.
	// Non-fatal: silently ignored when the file does not exist.
	_ = godotenv.Load()

	var cfgGlobal liverecservice.Config

	root := &cobra.Command{
		Use:     liverecservice.AppName,
		Short:   liverecservice.AppName,
		Version: appinfo.GetVersion(),
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			cfgFile := configpath.GetJSONConfigFilePath(liverecservice.AppName)

			cfg, err := liverecservice.LoadConfig(cfgFile)
			if err != nil {
				log.Warn().Err(err).Msg("Config not found. Creating default config")

				err := liverecservice.SaveConfig(cfgFile)
				if err != nil {
					return err
				}

				cfg, err = liverecservice.LoadConfig(cfgFile)
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
				logLevel = "info"
			}

			logFile := configpath.GetLogFilePath(liverecservice.AppName)

			closeLog, err := logger.InitLogger(logFile, logLevel)
			if err != nil {
				return err
			}

			defer closeLog()

			log.Info().
				Str("appName", liverecservice.AppName).
				Str("version", appinfo.GetVersion()).
				Str("logFile", logFile).
				Str("logLevel", logLevel).
				Msg("starting")

			return liverecservice.Run(liverecservice.AppName, cfgGlobal)
		},
	}

	err := root.Execute()
	if err != nil {
		log.Error().Err(err).Msg("command failed")
		os.Exit(1)
	}
}
