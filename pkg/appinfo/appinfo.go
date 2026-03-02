package appinfo

import "fmt"

var (
	Version   = "dev"     //nolint:gochecknoglobals
	GitCommit = "unknown" //nolint:gochecknoglobals
	BuildDate = "unknown" //nolint:gochecknoglobals
)

func String() string {
	return fmt.Sprintf("%s (commit=%s, built=%s)", Version, GitCommit, BuildDate)
}
