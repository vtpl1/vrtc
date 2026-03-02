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

func GetSessionFolder(applicationName string) string {
	return GetFolder(filepath.Join("session", applicationName))
}

func GetYAMLConfigFilePath(applicationName string) string {
	return filepath.Join(GetSessionFolder(applicationName), applicationName+".yaml")
}

func GetJSONConfigFilePath(applicationName string) string {
	return filepath.Join(GetSessionFolder(applicationName), applicationName+".json")
}

func GetTOMLConfigFilePath(applicationName string) string {
	return filepath.Join(GetSessionFolder(applicationName), applicationName+".toml")
}

func GetLogFilePath(applicationName string) string {
	return filepath.Join(GetLogFolder(applicationName), applicationName+".log")
}

func GetLogFolder(applicationName string) string {
	return GetFolder(filepath.Join("logs", applicationName))
}
