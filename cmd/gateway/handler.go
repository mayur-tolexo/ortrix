package main

import (
	"context"

	pb "github.com/mayur-tolexo/flowd/api/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GatewayHandler implements the GatewayService gRPC server.
type GatewayHandler struct {
	pb.UnimplementedGatewayServiceServer
	logger *logrus.Logger
}

// NewGatewayHandler creates a new GatewayHandler.
func NewGatewayHandler(logger *logrus.Logger) *GatewayHandler {
	return &GatewayHandler{logger: logger}
}

// SubmitTask accepts a task submission from a client.
// In the full implementation, this will:
//   - Validate the task payload
//   - Use the routing layer to determine the target partition
//   - Forward the task to the appropriate orchestrator instance
func (h *GatewayHandler) SubmitTask(_ context.Context, req *pb.SubmitTaskRequest) (*pb.SubmitTaskResponse, error) {
	if req.GetTask() == nil {
		return nil, status.Error(codes.InvalidArgument, "task must not be nil")
	}

	taskID := req.GetTask().GetId()
	h.logger.WithField("task_id", taskID).Info("task submitted")

	// TODO: Route task to the correct orchestrator partition via the routing layer.
	// TODO: Write task to WAL before acknowledging.

	return &pb.SubmitTaskResponse{
		TaskId:   taskID,
		Accepted: true,
		Message:  "task accepted",
	}, nil
}

// GetTaskStatus returns the status of a previously submitted task.
// In the full implementation, this will query the orchestrator for task state.
func (h *GatewayHandler) GetTaskStatus(_ context.Context, req *pb.TaskStatusRequest) (*pb.TaskStatusResponse, error) {
	taskID := req.GetTaskId()
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id must not be empty")
	}

	h.logger.WithField("task_id", taskID).Info("task status queried")

	// TODO: Query the orchestrator or state store for the actual task status.

	return &pb.TaskStatusResponse{
		TaskId: taskID,
		Status: "pending",
	}, nil
}
