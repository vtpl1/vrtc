package avftomp4

type Config struct {
	Input  string `json:"input,omitempty"  mapstructure:"input"  yaml:"input,omitempty"`
	Output string `json:"output,omitempty" mapstructure:"output" yaml:"output,omitempty"`
}
