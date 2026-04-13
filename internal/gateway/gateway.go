package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mayur-tolexo/ortrix/api/proto"
	"github.com/mayur-tolexo/ortrix/internal/executor"
	"github.com/mayur-tolexo/ortrix/internal/task"
)

// GatewayService implements the GatewayService gRPC interface.
type GatewayService struct {
	logger   logrus.FieldLogger
	engine   *executor.Engine
	taskIDMu sync.RWMutex

	proto.UnimplementedGatewayServiceServer
}

// WorkerServiceHandler implements the WorkerService gRPC interface for bidirectional streaming.
type WorkerServiceHandler struct {
	logger logrus.FieldLogger
	engine *executor.Engine
	mu     sync.RWMutex

	// Track connected workers
	workers map[string]*WorkerConnection

	proto.UnimplementedWorkerServiceServer
}

// WorkerConnection tracks an active worker connection.
type WorkerConnection struct {
	WorkerID      string
	Capabilities  []string
	ConnectedAt   time.Time
	LastSeen      time.Time
	TasksInFlight int
}

// NewGatewayService creates a new gateway service.
func NewGatewayService(logger logrus.FieldLogger, engine *executor.Engine) *GatewayService {
	if logger == nil {
		logger = logrus.New()
	}

	return &GatewayService{
		logger: logger.WithField("component", "gateway"),
		engine: engine,
	}
}

// SubmitTask implements the SubmitTask RPC.
func (gs *GatewayService) SubmitTask(ctx context.Context, req *proto.SubmitTaskRequest) (*proto.SubmitTaskResponse, error) {
	// Validate request
	if req == nil || req.Task == nil {
		gs.logger.Warn("SubmitTask: invalid request")
		return nil, status.Error(codes.InvalidArgument, "task cannot be nil")
	}

	if req.Task.Type == "" {
		gs.logger.Warn("SubmitTask: missing task type")
		return nil, status.Error(codes.InvalidArgument, "task type is required")
	}

	if req.Task.Id == "" {
		gs.logger.Warn("SubmitTask: missing task ID")
		return nil, status.Error(codes.InvalidArgument, "task ID is required")
	}

	gs.logger.WithFields(logrus.Fields{
		"task_id":   req.Task.Id,
		"task_type": req.Task.Type,
	}).Info("Submitting task")

	// Generate a workflow ID for this task
	// In a real system, this could be extracted from request context or headers
	workflowID := fmt.Sprintf("workflow-%d", time.Now().UnixNano())

	// Create task in engine
	t, err := gs.engine.CreateTask(workflowID, req.Task.Type, req.Task.Payload)
	if err != nil {
		gs.logger.WithError(err).Error("Failed to create task")
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create task: %v", err))
	}

	gs.logger.WithField("task_id", t.ID).Info("Task submitted successfully")

	return &proto.SubmitTaskResponse{
		TaskId:   t.ID,
		Accepted: true,
		Message:  "Task accepted for execution",
	}, nil
}

// GetTaskStatus implements the GetTaskStatus RPC.
func (gs *GatewayService) GetTaskStatus(ctx context.Context, req *proto.TaskStatusRequest) (*proto.TaskStatusResponse, error) {
	// Validate request
	if req == nil || req.TaskId == "" {
		gs.logger.Warn("GetTaskStatus: missing task ID")
		return nil, status.Error(codes.InvalidArgument, "task ID is required")
	}

	gs.logger.WithField("task_id", req.TaskId).Debug("Getting task status")

	// Get task from engine
	t, err := gs.engine.GetTask(req.TaskId)
	if err != nil {
		gs.logger.WithError(err).Warn("Task not found")
		return nil, status.Error(codes.NotFound, fmt.Sprintf("task not found: %v", err))
	}

	if t == nil {
		gs.logger.Warn("GetTaskStatus: task is nil")
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// Build response
	resp := &proto.TaskStatusResponse{
		TaskId: t.ID,
		Status: string(t.State),
	}

	// Add result if task is completed
	if t.State == task.StateCompleted || t.State == task.StateFailed {
		resp.Result = &proto.TaskResult{
			TaskId:      t.ID,
			Success:     t.State == task.StateCompleted,
			Output:      t.Result,
			Error:       t.Error,
			CompletedAt: t.UpdatedAt.Unix(),
		}
	}

	return resp, nil
}

// NewWorkerServiceHandler creates a new worker service handler.
func NewWorkerServiceHandler(logger logrus.FieldLogger, engine *executor.Engine) *WorkerServiceHandler {
	if logger == nil {
		logger = logrus.New()
	}

	return &WorkerServiceHandler{
		logger:  logger.WithField("component", "worker_service"),
		engine:  engine,
		workers: make(map[string]*WorkerConnection),
	}
}

// StreamTasks implements the StreamTasks RPC for bidirectional streaming.
// This is a stub that demonstrates the interface. Full implementation requires
// gRPC server setup with proper message types.
func (wsh *WorkerServiceHandler) StreamTasks(stream grpc.ServerStream) error {
	wsh.logger.Debug("New worker stream connected")

	// Note: This is a placeholder implementation.
	// Full implementation would require:
	// 1. Proper gRPC service implementation
	// 2. Type-safe message casting
	// 3. Full streaming loop

	return nil
}

// handleTaskResult processes a task result from a worker.
func (wsh *WorkerServiceHandler) handleTaskResult(result *proto.TaskResult) {
	if result == nil {
		return
	}

	wsh.logger.WithFields(logrus.Fields{
		"task_id": result.TaskId,
		"success": result.Success,
	}).Debug("Processing task result")

	if result.Success {
		_, err := wsh.engine.CompleteTask(result.TaskId, result.Output)
		if err != nil {
			wsh.logger.WithError(err).Error("Failed to complete task")
		}
	} else {
		_, err := wsh.engine.FailTask(result.TaskId, result.Error)
		if err != nil {
			wsh.logger.WithError(err).Error("Failed to fail task")
		}
	}
}

// GetWorkerStats returns statistics about connected workers.
func (wsh *WorkerServiceHandler) GetWorkerStats() map[string]interface{} {
	wsh.mu.RLock()
	defer wsh.mu.RUnlock()

	stats := map[string]interface{}{
		"total_workers": len(wsh.workers),
		"workers":       make([]map[string]interface{}, 0),
	}

	workers := make([]map[string]interface{}, 0, len(wsh.workers))
	for _, w := range wsh.workers {
		workers = append(workers, map[string]interface{}{
			"worker_id":       w.WorkerID,
			"capabilities":    w.Capabilities,
			"connected_at":    w.ConnectedAt,
			"last_seen":       w.LastSeen,
			"tasks_in_flight": w.TasksInFlight,
		})
	}

	stats["workers"] = workers
	return stats
}
