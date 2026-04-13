// Package main is the entry point for the Flowd gateway service.
// The gateway exposes a gRPC API for external clients to submit tasks
// and query task status.
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

	logger.WithField("port", cfg.GatewayPort).Info("starting flowd gateway")

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GatewayPort))
	if err != nil {
		logger.WithError(err).Fatal("failed to listen")
	}

	grpcServer := grpc.NewServer()
	gatewayHandler := NewGatewayHandler(logger)
	pb.RegisterGatewayServiceServer(grpcServer, gatewayHandler)

	// Enable gRPC reflection for development tooling.
	reflection.Register(grpcServer)

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.WithField("signal", sig.String()).Info("shutting down gateway")
		grpcServer.GracefulStop()
	}()

	logger.Info("gateway gRPC server listening")
	if err := grpcServer.Serve(lis); err != nil {
		logger.WithError(err).Fatal("gateway server failed")
	}
}
