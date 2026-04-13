package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Unset any env vars that might be set.
	os.Unsetenv("ORTRIX_GATEWAY_PORT")
	os.Unsetenv("ORTRIX_ORCHESTRATOR_PORT")
	os.Unsetenv("ORTRIX_ORCHESTRATOR_ADDR")
	os.Unsetenv("ORTRIX_LOG_LEVEL")
	os.Unsetenv("ORTRIX_ENVIRONMENT")

	cfg := Load()

	if cfg.GatewayPort != 8080 {
		t.Errorf("expected GatewayPort 8080, got %d", cfg.GatewayPort)
	}
	if cfg.OrchestratorPort != 9090 {
		t.Errorf("expected OrchestratorPort 9090, got %d", cfg.OrchestratorPort)
	}
	if cfg.OrchestratorAddr != "localhost:9090" {
		t.Errorf("expected OrchestratorAddr localhost:9090, got %s", cfg.OrchestratorAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel debug, got %s", cfg.LogLevel)
	}
	if cfg.Environment != "development" {
		t.Errorf("expected Environment development, got %s", cfg.Environment)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("ORTRIX_GATEWAY_PORT", "3000")
	t.Setenv("ORTRIX_ORCHESTRATOR_PORT", "4000")
	t.Setenv("ORTRIX_ORCHESTRATOR_ADDR", "orch:4000")
	t.Setenv("ORTRIX_LOG_LEVEL", "info")
	t.Setenv("ORTRIX_ENVIRONMENT", "production")

	cfg := Load()

	if cfg.GatewayPort != 3000 {
		t.Errorf("expected GatewayPort 3000, got %d", cfg.GatewayPort)
	}
	if cfg.OrchestratorPort != 4000 {
		t.Errorf("expected OrchestratorPort 4000, got %d", cfg.OrchestratorPort)
	}
	if cfg.OrchestratorAddr != "orch:4000" {
		t.Errorf("expected OrchestratorAddr orch:4000, got %s", cfg.OrchestratorAddr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel info, got %s", cfg.LogLevel)
	}
	if cfg.Environment != "production" {
		t.Errorf("expected Environment production, got %s", cfg.Environment)
	}
}

func TestGetEnvIntInvalid(t *testing.T) {
	t.Setenv("ORTRIX_GATEWAY_PORT", "not-a-number")

	cfg := Load()

	if cfg.GatewayPort != 8080 {
		t.Errorf("expected default GatewayPort 8080 for invalid env, got %d", cfg.GatewayPort)
	}
}
