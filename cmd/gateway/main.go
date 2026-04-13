// Package main is the entry point for the Ortrix gateway service.
// The gateway exposes a gRPC API for external clients to submit tasks
// and query task status. It also serves a REST API with Swagger
// documentation when enabled.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mayur-tolexo/ortrix/internal/config"
	"github.com/mayur-tolexo/ortrix/internal/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/mayur-tolexo/ortrix/api/proto"

	// Import generated Swagger docs so they are available at runtime.
	_ "github.com/mayur-tolexo/ortrix/docs/swagger"

	httpSwagger "github.com/swaggo/http-swagger"
)

// @title Ortrix Gateway API
// @version 1.0
// @description REST API for the Ortrix distributed workflow orchestrator.
// @termsOfService http://swagger.io/terms/

// @contact.name Ortrix Maintainers
// @contact.url https://github.com/mayur-tolexo/ortrix

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8080
// @BasePath /

func main() {
	cfg := config.Load()
	logger := logging.NewLogger(cfg.LogLevel)

	logger.WithField("port", cfg.GatewayPort).Info("starting ortrix gateway")

	// ── gRPC server ──────────────────────────────────────────────

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GatewayPort))
	if err != nil {
		logger.WithError(err).Fatal("failed to listen")
	}

	grpcServer := grpc.NewServer()
	gatewayHandler := NewGatewayHandler(logger)
	pb.RegisterGatewayServiceServer(grpcServer, gatewayHandler)

	// Enable gRPC reflection for development tooling.
	reflection.Register(grpcServer)

	// ── HTTP/REST server with Swagger ────────────────────────────

	httpHandler := NewHTTPHandler(logger)
	mux := http.NewServeMux()

	// REST API routes.
	mux.HandleFunc("/api/v1/workflows/start", httpHandler.StartWorkflow)
	mux.HandleFunc("/api/v1/workflows/", httpHandler.GetWorkflowStatus)
	mux.HandleFunc("/healthz", httpHandler.HealthCheck)

	// Serve Swagger UI when enabled (typically in development).
	if cfg.SwaggerEnabled {
		logger.Info("swagger UI enabled at /swagger/index.html")
		mux.Handle("/swagger/", httpSwagger.WrapHandler)
	}

	httpPort := cfg.GatewayPort + 1 // HTTP on port 8081 by default
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", httpPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.WithField("port", httpPort).Info("gateway HTTP server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Fatal("HTTP server failed")
		}
	}()

	// ── Graceful shutdown ────────────────────────────────────────

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.WithField("signal", sig.String()).Info("shutting down gateway")
		grpcServer.GracefulStop()
		if err := httpServer.Shutdown(context.Background()); err != nil {
			logger.WithError(err).Error("HTTP server shutdown error")
		}
	}()

	logger.Info("gateway gRPC server listening")
	if err := grpcServer.Serve(lis); err != nil {
		logger.WithError(err).Fatal("gateway server failed")
	}
}
