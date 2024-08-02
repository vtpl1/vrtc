package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// LoadConfig loads configuration
func LoadConfig(v any) {
	for _, data := range configs {
		if err := Unmarshal(data, v); err != nil {
			Logger.Warn().Err(err).Send()
		}
	}
}

var errConfigFileDisabled = errors.New("config file disabled")

// PatchConfig loads configuration
func PatchConfig(key string, value any, path ...string) error {
	if ConfigPath == "" {
		return errConfigFileDisabled
	}

	// empty config is OK
	b, _ := os.ReadFile(ConfigPath)

	b, err := Patch(b, key, value, path...)
	if err != nil {
		return err
	}

	return os.WriteFile(ConfigPath, b, 0o644) //nolint:gosec
}

type flagConfig []string

func (c *flagConfig) String() string {
	return strings.Join(*c, " ")
}

func (c *flagConfig) Set(value string) error {
	*c = append(*c, value)
	return nil
}

var configs [][]byte

func initConfig(confs flagConfig) {
	if confs == nil {
		confs = []string{"vrtc.yaml"}
	}

	for _, conf := range confs {
		if len(conf) == 0 {
			continue
		}
		if conf[0] == '{' {
			// config as raw YAML or JSON
			configs = append(configs, []byte(conf))
		} else if data := parseConfString(conf); data != nil {
			configs = append(configs, data)
		} else {
			// config as file
			if ConfigPath == "" {
				ConfigPath = conf
			}

			if data, _ = os.ReadFile(conf); data == nil { //nolint:gosec
				continue
			}

			data = []byte(ReplaceEnvVars(string(data)))
			configs = append(configs, data)
		}
	}

	if ConfigPath != "" {
		if !filepath.IsAbs(ConfigPath) {
			if cwd, err := os.Getwd(); err == nil {
				ConfigPath = filepath.Join(cwd, ConfigPath)
			}
		}
		Info["config_path"] = ConfigPath
	}
}

func parseConfString(s string) []byte {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return nil
	}

	items := strings.Split(s[:i], ".")
	if len(items) < 2 {
		return nil
	}

	// `log.level=trace` => `{log: {level: trace}}`
	var pre string
	suf := s[i+1:]
	for _, item := range items {
		pre += "{" + item + ": "
		suf += "}"
	}

	return []byte(pre + suf)
}
