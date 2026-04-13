// Package main is the entry point for the Flowd orchestrator service.
// The orchestrator manages task scheduling, dispatches tasks to workers
// via bidirectional gRPC streaming, and coordinates with the WAL and
// partition manager for durability and distribution.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/mayur-tolexo/flowd/internal/config"
	"github.com/mayur-tolexo/flowd/internal/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/mayur-tolexo/flowd/api/proto"
)

func main() {
	cfg := config.Load()
	logger := logging.NewLogger(cfg.LogLevel)

	logger.WithField("port", cfg.OrchestratorPort).Info("starting flowd orchestrator")

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.OrchestratorPort))
	if err != nil {
		logger.WithError(err).Fatal("failed to listen")
	}

	grpcServer := grpc.NewServer()
	workerHandler := NewWorkerHandler(logger)
	pb.RegisterWorkerServiceServer(grpcServer, workerHandler)

	// Enable gRPC reflection for development tooling.
	reflection.Register(grpcServer)

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.WithField("signal", sig.String()).Info("shutting down orchestrator")
		grpcServer.GracefulStop()
	}()

	logger.Info("orchestrator gRPC server listening")
	if err := grpcServer.Serve(lis); err != nil {
		logger.WithError(err).Fatal("orchestrator server failed")
	}
}
