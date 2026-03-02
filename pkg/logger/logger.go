package logger

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/diode"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

const maxLogBuffer = 1000

type CloseFunc func()

// InitLogger initialises the logger with zerolog, diode, and a rotating logger.
func InitLogger(logFile, logLevel string) (CloseFunc, error) {
	// Configure Lumberjack for log rotation
	rotatingLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10, // Max size in MB before rotation
		MaxBackups: 3,  // Max number of old log files to keep
		MaxAge:     28, // Max number of days to retain old log files
		// Compress:   true, // Compress rotated files
	}

	// Wrap Lumberjack with Diode for non-blocking logging
	diodeWriter := diode.NewWriter(rotatingLogger, maxLogBuffer, 0, func(missed int) {
		fmt.Printf("Dropped %d log messages due to buffer overflow\n", missed) //nolint:forbidigo
	})
	// Wrap diode writer with ConsoleWriter for human-readable output
	consoleWriter := zerolog.ConsoleWriter{
		Out:        diodeWriter,
		TimeFormat: time.RFC3339,
		NoColor:    true, // disable color for file output
	}

	// Build logger
	logger := zerolog.New(consoleWriter).With().Timestamp().Logger()

	log.Logger = logger

	// Set log level
	level, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		fmt.Printf("Invalid log level: %s\n", logLevel) //nolint:forbidigo

		return func() { _ = diodeWriter.Close() }, fmt.Errorf("InitLogger error %w", err)
	}

	zerolog.SetGlobalLevel(level)

	return func() { _ = diodeWriter.Close() }, nil
}
