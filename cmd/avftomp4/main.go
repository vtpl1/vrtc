package main

import (
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vtpl1/vrtc/internal/avftomp4"
	"github.com/vtpl1/vrtc/pkg/appinfo"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
	})

	err := Execute()
	if err != nil {
		log.Error().Err(err).Msg("command failed")
		os.Exit(1)
	}
}

var errNoInputFileProvided = errors.New("no input file provided")

func newRootCmd() *cobra.Command {
	var cfgFile string

	cmd := &cobra.Command{
		Use:     avftomp4.AppName + " -i <input> [output]",
		Short:   avftomp4.AppName,
		Version: appinfo.GetVersion(),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return avftomp4.InitViper(cmd, cfgFile)
		},
		RunE: runRoot,
	}

	cmd.Flags().StringP("input", "i", "", "Input AVF file (required)")
	cmd.Flags().StringP("output", "o", "", "Output MP4 file")
	cmd.Flags().StringVar(&cfgFile, "config", "", "Config file (yaml)")

	return cmd
}

func runRoot(_ *cobra.Command, args []string) error {
	input := viper.GetString("input")
	if input == "" {
		return fmt.Errorf("%w (use -i <input> [output])", errNoInputFileProvided)
	}

	output := resolveOutput(input, viper.GetString("output"), args)

	cfg := avftomp4.Config{
		Input:  input,
		Output: output,
	}

	return avftomp4.Run(cfg)
}

func resolveOutput(input, flagValue string, args []string) string {
	if flagValue != "" {
		return flagValue
	}

	if len(args) > 0 {
		return args[0]
	}

	base := path.Base(input)

	ext := path.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}

	return base + ".mp4"
}

func Execute() error {
	return newRootCmd().Execute()
}
