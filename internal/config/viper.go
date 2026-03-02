package config

import (
	"strings"

	"github.com/spf13/viper"
)

// LoadConfigYAML reads a YAML file at cfgFile and unmarshals it into T.
func LoadConfigYAML[T any](cfgFile string) (*T, error) {
	return loadConfig[T](cfgFile, "yaml")
}

// LoadConfigJSON reads a JSON file at cfgFile and unmarshals it into T.
func LoadConfigJSON[T any](cfgFile string) (*T, error) {
	return loadConfig[T](cfgFile, "json")
}

func loadConfig[T any](cfgFile, format string) (*T, error) {
	v := viper.New()

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetConfigFile(cfgFile)
	v.SetConfigType(format)

	err := v.ReadInConfig()
	if err != nil {
		return nil, err
	}

	var cfg T

	err = v.Unmarshal(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// SaveConfigYAML writes key/value pairs to cfgFile as YAML via viper.
func SaveConfigYAML(cfgFile string, keys map[string]any) error {
	return saveConfig(cfgFile, "yaml", keys)
}

// SaveConfigJSON writes key/value pairs to cfgFile as JSON via viper.
func SaveConfigJSON(cfgFile string, keys map[string]any) error {
	return saveConfig(cfgFile, "json", keys)
}

func saveConfig(cfgFile, format string, keys map[string]any) error {
	v := viper.New()
	v.SetConfigFile(cfgFile)
	v.SetConfigType(format)

	for k, val := range keys {
		v.Set(k, val)
	}

	return v.WriteConfigAs(cfgFile)
}
