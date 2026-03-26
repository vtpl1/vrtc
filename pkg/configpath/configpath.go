package configpath

import (
	"fmt"
	"os"
	"path/filepath"
)

func GetFolder(folder string) string {
	err := os.MkdirAll(folder, 0o750)
	if err != nil {
		fmt.Printf("Unable to create folder %s, %v", folder, err) //nolint:forbidigo
	}

	return folder
}

func GetConfigFolder(applicationName string) string {
	return GetFolder(filepath.Join("config", applicationName))
}

func GetSessionFolder(applicationName string) string {
	return GetFolder(filepath.Join("session", applicationName))
}

func GetYAMLConfigFilePath(applicationName string) string {
	return filepath.Join(GetConfigFolder(applicationName), applicationName+".yaml")
}

func GetJSONConfigFilePath(applicationName string) string {
	return filepath.Join(GetConfigFolder(applicationName), applicationName+".json")
}

func GetTOMLConfigFilePath(applicationName string) string {
	return filepath.Join(GetConfigFolder(applicationName), applicationName+".toml")
}

func GetJSONSessionFilePath(applicationName string) string {
	return filepath.Join(GetSessionFolder(applicationName), applicationName+".json")
}

func GetLogFilePath(applicationName string) string {
	return filepath.Join(GetLogFolder(applicationName), applicationName+".log")
}

func GetLogFolder(applicationName string) string {
	return GetFolder(filepath.Join("logs", applicationName))
}
