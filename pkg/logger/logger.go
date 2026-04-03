package logger

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/diode"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

const maxLogBuffer = 1000

type CloseFunc func()

// InitLogger initialises the logger with zerolog, diode, and a rotating logger.
// Output goes to both the log file and stdout (console).
// Output goes to both the log file and stdout (console).
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

	// File writer: human-readable, no color
	fileWriter := zerolog.ConsoleWriter{
		Out:        diodeWriter,
		TimeFormat: time.RFC3339,
		NoColor:    true,
	}

	// Console writer: human-readable, with color
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.RFC3339,
	}

	// Build logger writing to both file and console
	multi := io.MultiWriter(fileWriter, consoleWriter)
	logger := zerolog.New(multi).With().Timestamp().Caller().Logger()

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
