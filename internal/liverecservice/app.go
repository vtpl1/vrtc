package liverecservice

import (
	"encoding/json"
	"fmt"
)

const AppName = "entrypoint_live_recording"

func Run(_ string, cfg Config) error {
	jsonData, _ := json.Marshal(cfg)
	fmt.Println(string(jsonData))
	return nil
}
