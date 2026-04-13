package logging

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestNewLoggerDebug(t *testing.T) {
	logger := NewLogger("debug")
	if logger.GetLevel() != logrus.DebugLevel {
		t.Errorf("expected DebugLevel, got %v", logger.GetLevel())
	}
}

func TestNewLoggerInfo(t *testing.T) {
	logger := NewLogger("info")
	if logger.GetLevel() != logrus.InfoLevel {
		t.Errorf("expected InfoLevel, got %v", logger.GetLevel())
	}
}

func TestNewLoggerInvalidFallsBackToDebug(t *testing.T) {
	logger := NewLogger("invalid-level")
	if logger.GetLevel() != logrus.DebugLevel {
		t.Errorf("expected DebugLevel for invalid level, got %v", logger.GetLevel())
	}
}
