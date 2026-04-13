// Package config provides configuration loading for Ortrix services.
// Configuration is loaded from environment variables with optional defaults.
package config

import (
	"os"
	"strconv"
)

// Config holds the application configuration shared across services.
type Config struct {
	// GatewayPort is the port the gateway gRPC server listens on.
	GatewayPort int

	// OrchestratorPort is the port the orchestrator gRPC server listens on.
	OrchestratorPort int

	// OrchestratorAddr is the address of the orchestrator for workers to connect to.
	OrchestratorAddr string

	// LogLevel controls the structured logging level (debug, info, warn, error).
	LogLevel string

	// Environment is the deployment environment (development, staging, production).
	Environment string

	// SwaggerEnabled controls whether the Swagger UI is served by the gateway.
	// Defaults to true in development, false otherwise.
	SwaggerEnabled bool
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	env := getEnv("ORTRIX_ENVIRONMENT", "development")
	return &Config{
		GatewayPort:      getEnvInt("ORTRIX_GATEWAY_PORT", 8080),
		OrchestratorPort: getEnvInt("ORTRIX_ORCHESTRATOR_PORT", 9090),
		OrchestratorAddr: getEnv("ORTRIX_ORCHESTRATOR_ADDR", "localhost:9090"),
		LogLevel:         getEnv("ORTRIX_LOG_LEVEL", "debug"),
		Environment:      env,
		SwaggerEnabled:   getEnvBool("ORTRIX_SWAGGER_ENABLED", env == "development"),
	}
}

// getEnv returns the value of an environment variable or a default.
func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

// getEnvInt returns the integer value of an environment variable or a default.
func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

// getEnvBool returns the boolean value of an environment variable or a default.
func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}
	return defaultVal
}
