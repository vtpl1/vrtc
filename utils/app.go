package utils

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
)

var (
	Version    string                    // Version of the application
	UserAgent  string                    // UserAgent of the application
	ConfigPath string                    // ConfigPath is the configuration file path to be supplied from outside
	Info       = make(map[string]string) // Info holds some global configuration
)

const usage = `Usage of vrtc:

  -c, --config   Path to config file or config string as YAML or JSON, support multiple
  -d, --daemon   Run in background
  -v, --version  Print version and exit
`

var (
	errVersionRequested                  = errors.New("version requested")
	errDaemonModeIsNotSupportedOnWindows = errors.New("daemon mode is not supported on Windows")
	errRunningInDaemonMode               = errors.New("running in daemon mode")
)

// Init is the entrypoint
func Init() error {
	var config flagConfig
	var daemon bool
	var version bool

	flag.Var(&config, "config", "")
	flag.Var(&config, "c", "")
	flag.BoolVar(&daemon, "daemon", false, "")
	flag.BoolVar(&daemon, "d", false, "")
	flag.BoolVar(&version, "version", false, "")
	flag.BoolVar(&version, "v", false, "")

	flag.Usage = func() {
		fmt.Print(usage) //nolint:forbidigo
	}
	flag.Parse()

	revision, vcsTime := readRevisionTime()

	if version {
		fmt.Printf("vrtc version %s (%s) %s/%s\n", Version, revision, runtime.GOOS, runtime.GOARCH) //nolint:forbidigo
		return errVersionRequested
	}

	if daemon && os.Getppid() != 1 {
		if runtime.GOOS == "windows" {
			fmt.Println("Daemon mode is not supported on Windows") //nolint:forbidigo
			return errDaemonModeIsNotSupportedOnWindows
		}

		// Re-run the program in background and exit
		cmd := exec.Command(os.Args[0], os.Args[1:]...) //nolint:gosec
		if err := cmd.Start(); err != nil {
			fmt.Println("Failed to start daemon:", err) //nolint:forbidigo
			return err
		}
		fmt.Println("Running in daemon mode with PID:", cmd.Process.Pid) //nolint:forbidigo
		return errRunningInDaemonMode
	}

	UserAgent = "vrtc/" + Version

	Info["version"] = Version
	Info["revision"] = revision

	initConfig(config)
	initLogger()

	platform := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	Logger.Info().Str("version", Version).Str("platform", platform).Str("revision", revision).Msg("vrtc")
	Logger.Debug().Str("version", runtime.Version()).Str("vcs.time", vcsTime).Msg("build")

	if ConfigPath != "" {
		Logger.Info().Str("path", ConfigPath).Msg("config")
	}
	return nil
}

// InternalTerminationRequest is a channel to signal termination
var InternalTerminationRequest chan int

func readRevisionTime() (revision, vcsTime string) {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if len(setting.Value) > 7 {
					revision = setting.Value[:7]
				} else {
					revision = setting.Value
				}
			case "vcs.time":
				vcsTime = setting.Value
			case "vcs.modified":
				if setting.Value == "true" {
					revision = "mod." + revision
				}
			}
		}
	}
	return
}
