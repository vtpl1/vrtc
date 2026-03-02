package avftomp4

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func InitViper(cmd *cobra.Command, cfgFile string) error {
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	// Defaults
	viper.SetDefault("input", "")
	viper.SetDefault("output", "")

	err := viper.BindPFlags(cmd.Flags())
	if err != nil {
		return err
	}

	if cfgFile == "" {
		return nil
	}

	viper.SetConfigFile(cfgFile)

	err = viper.ReadInConfig()
	if err != nil {
		return err
	}

	log.Info().
		Str("config", viper.ConfigFileUsed()).
		Msg("Using config file")

	return nil
}
