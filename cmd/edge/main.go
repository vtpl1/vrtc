package main

import (
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/vtpl1/vrtc/internal/edge"
	"github.com/vtpl1/vrtc/pkg/appinfo"
	"github.com/vtpl1/vrtc/pkg/configpath"
)

func main() {
	// Load .env into the process environment before Viper reads it.
	// Non-fatal: silently ignored when the file does not exist.
	_ = godotenv.Load()

	var cfgGlobal edge.Config

	root := &cobra.Command{
		Use:     edge.AppName,
		Short:   edge.AppName,
		Version: appinfo.GetVersion(),
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			cfgFile := configpath.GetYAMLConfigFilePath(edge.AppName)

			cfg, err := edge.LoadConfig(cfgFile)
			if err != nil {
				log.Warn().Err(err).Msg("Config not found. Creating default config")

				err := edge.SaveConfig(cfgFile)
				if err != nil {
					return err
				}

				cfg, err = edge.LoadConfig(cfgFile)
				if err != nil {
					return err
				}
			}

			cfgGlobal = *cfg

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return edge.Run(edge.AppName, "edge", cfgGlobal)
		},
	}

	err := root.Execute()
	if err != nil {
		log.Error().Err(err).Msg("command failed")
		os.Exit(1)
	}
}
