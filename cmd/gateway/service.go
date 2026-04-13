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
	WorkerID     string
	Capabilities []string
	ConnectedAt  time.Time
	LastSeen     time.Time
	TasKsInFlight int
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
// godoc
// @Summary Submit a new task
// @Description Submit a task for execution
// @Accept json
// @Produce json
// @Param request body proto.SubmitTaskRequest true "Task submission request"
// @Success 200 {object} proto.SubmitTaskResponse
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Internal server error"
// @Router /v1/tasks [post]
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

	// Create task in engine
	t, err := gs.engine.CreateTask("", req.Task.Type, req.Task.Payload)
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
// godoc
// @Summary Get task status
// @Description Get the status and result of a task
// @Produce json
// @Param task_id path string true "Task ID"
// @Success 200 {object} proto.TaskStatusResponse
// @Failure 404 {string} string "Task not found"
// @Failure 500 {string} string "Internal server error"
// @Router /v1/tasks/{task_id}/status [get]
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
		Status: t.State.String(),
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
func (wsh *WorkerServiceHandler) StreamTasks(stream grpc.ServerStream) error {
	wsh.logger.Debug("New worker stream connected")

	ctx := stream.Context()
	var workerID string
	var capabilities []string

	// First message should be registration
	msg, err := stream.Recv()
	if err != nil {
		wsh.logger.WithError(err).Error("Failed to receive first message")
		return err
	}

	// Process registration
	reg := msg.GetRegistration()
	if reg == nil {
		wsh.logger.Error("First message must be registration")
		return status.Error(codes.FailedPrecondition, "first message must be registration")
	}

	workerID = msg.WorkerId
	capabilities = reg.Capabilities

	if workerID == "" {
		wsh.logger.Error("Worker ID is required")
		return status.Error(codes.InvalidArgument, "worker ID is required")
	}

	wsh.logger.WithFields(logrus.Fields{
		"worker_id":     workerID,
		"capabilities":  capabilities,
		"num_services":  len(capabilities),
	}).Info("Worker registered")

	// Record worker connection
	wsh.mu.Lock()
	wsh.workers[workerID] = &WorkerConnection{
		WorkerID:     workerID,
		Capabilities: capabilities,
		ConnectedAt:  time.Now(),
		LastSeen:     time.Now(),
	}
	wsh.mu.Unlock()

	defer func() {
		wsh.mu.Lock()
		delete(wsh.workers, workerID)
		wsh.mu.Unlock()

		wsh.logger.WithField("worker_id", workerID).Info("Worker disconnected")
	}()

	// Process messages from worker
	for {
		select {
		case <-ctx.Done():
			wsh.logger.WithField("worker_id", workerID).Info("Context cancelled")
			return ctx.Err()
		default:
		}

		// Receive message from worker
		msg, err := stream.Recv()
		if err != nil {
			wsh.logger.WithError(err).Warn("Failed to receive message from worker")
			return err
		}

		// Update last seen
		wsh.mu.Lock()
		if w, ok := wsh.workers[workerID]; ok {
			w.LastSeen = time.Now()
		}
		wsh.mu.Unlock()

		// Handle message
		switch payload := msg.Payload.(type) {
		case *proto.WorkerMessage_Result:
			wsh.handleTaskResult(payload.Result)

		case *proto.WorkerMessage_Heartbeat:
			wsh.logger.WithField("worker_id", msg.WorkerId).Debug("Heartbeat received")

		case *proto.WorkerMessage_Registration:
			// Update registration
			wsh.logger.WithField("worker_id", msg.WorkerId).Debug("Registration update received")

		default:
			wsh.logger.WithField("type", fmt.Sprintf("%T", msg.Payload)).Warn("Unknown message type")
		}

		// Send acknowledgement
		ack := &proto.OrchestratorMessage{
			Payload: &proto.OrchestratorMessage_Ack{
				Ack: &proto.Ack{
					Message: "message received",
				},
			},
		}

		if err := stream.Send(ack); err != nil {
			wsh.logger.WithError(err).Error("Failed to send acknowledgement")
			return err
		}
	}
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
		err := wsh.engine.CompleteTask(result.TaskId, result.Output)
		if err != nil {
			wsh.logger.WithError(err).Error("Failed to complete task")
		}
	} else {
		err := wsh.engine.FailTask(result.TaskId, result.Error)
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
			"worker_id":      w.WorkerID,
			"capabilities":   w.Capabilities,
			"connected_at":   w.ConnectedAt,
			"last_seen":      w.LastSeen,
			"tasks_in_flight": w.TasKsInFlight,
		})
	}

	stats["workers"] = workers
	return stats
}
