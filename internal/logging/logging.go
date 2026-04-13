// Package logging provides structured logging for Flowd services.
// It uses logrus with JSON formatting for production-grade log output.
package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

// NewLogger creates a new structured logger configured with the given level.
// Supported levels: debug, info, warn, error. Defaults to debug.
func NewLogger(level string) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
	})

	parsed, err := logrus.ParseLevel(level)
	if err != nil {
		parsed = logrus.DebugLevel
	}
	logger.SetLevel(parsed)

	return logger
}
