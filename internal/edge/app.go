package edge

import (
	"encoding/json"
	"fmt"
)

func Run(_, _ string, cfg Config) error {
	jsonData, _ := json.Marshal(cfg)
	fmt.Println(string(jsonData))
	return nil
}
