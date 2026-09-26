package logging

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// New creates a structured JSON logger using the configured log level.
// Supported levels are debug, info, warn, and error.
func New(w io.Writer, level string) (zerolog.Logger, error) {
	logLevel, err := parseLevel(level)
	if err != nil {
		return zerolog.Logger{}, err
	}

	logger := zerolog.New(newRedactingWriter(w)).
		Level(logLevel).
		With().
		Timestamp().
		Logger()

	if logLevel == zerolog.DebugLevel {
		logger = logger.Hook(errorStackHook{})
	}

	return logger, nil
}

type errorStackHook struct{}

func (errorStackHook) Run(event *zerolog.Event, level zerolog.Level, message string) {
	if level == zerolog.ErrorLevel {
		event.Str("stack", string(debug.Stack()))
	}
}

func parseLevel(level string) (zerolog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zerolog.DebugLevel, nil
	case "", "info":
		return zerolog.InfoLevel, nil
	case "warn":
		return zerolog.WarnLevel, nil
	case "error":
		return zerolog.ErrorLevel, nil
	default:
		return zerolog.InfoLevel, fmt.Errorf(
			"invalid LOG_LEVEL %q: expected debug, info, warn, or error",
			level,
		)
	}
}

func init() {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano
}
