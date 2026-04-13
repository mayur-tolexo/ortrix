// Package sdk provides the worker SDK for Ortrix.
// Workers use this SDK to register capabilities, connect to the orchestrator,
// and receive tasks for execution over bidirectional gRPC streams.
package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/mayur-tolexo/ortrix/api/proto"
	"github.com/mayur-tolexo/ortrix/internal/streaming"
)

// TaskHandler is the callback function for handling tasks.
type TaskHandler func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error)

// WorkerConfig holds configuration for the worker SDK.
type WorkerConfig struct {
	// WorkerID is the unique identifier for this worker.
	WorkerID string

	// ServerAddress is the address of the orchestrator server.
	ServerAddress string

	// Capabilities are the task types this worker can handle.
	Capabilities []string

	// StreamConfig is the configuration for stream management.
	StreamConfig streaming.StreamConfig

	// Logger is the logger instance.
	Logger logrus.FieldLogger
}

// Worker represents an embedded worker SDK instance.
type Worker struct {
	config   WorkerConfig
	logger   logrus.FieldLogger
	sm       *streaming.StreamManager
	handlers map[string]TaskHandler
	mu       sync.RWMutex

	// Stream state
	client       proto.WorkerService_StreamTasksClient
	clientMu     sync.Mutex
	connected    bool
	connectedMu  sync.RWMutex

	// Context and lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// Statistics
	stats *WorkerStats
}

// WorkerStats holds worker statistics.
type WorkerStats struct {
	mu                sync.RWMutex
	TasksProcessed    int64
	TasksFailed       int64
	TotalProcessTime  time.Duration
	AverageTime       time.Duration
	LastProcessedTime time.Time
}

// NewWorker creates a new worker instance.
func NewWorker(config WorkerConfig) (*Worker, error) {
	if config.WorkerID == "" {
		return nil, fmt.Errorf("worker ID is required")
	}
	if config.ServerAddress == "" {
		return nil, fmt.Errorf("server address is required")
	}
	if config.Logger == nil {
		config.Logger = logrus.New()
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Worker{
		config:    config,
		logger:    config.Logger.WithField("worker_id", config.WorkerID),
		sm:        streaming.NewStreamManager(config.Logger, config.StreamConfig),
		handlers:  make(map[string]TaskHandler),
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		stats:     &WorkerStats{},
		connected: false,
	}

	w.logger.Info("Worker initialized")
	return w, nil
}

// RegisterHandler registers a task handler for a specific task type.
func (w *Worker) RegisterHandler(taskType string, handler TaskHandler) error {
	if taskType == "" {
		return fmt.Errorf("task type is required")
	}
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.handlers[taskType] = handler
	w.logger.WithField("task_type", taskType).Info("Handler registered")
	return nil
}

// Start connects to the orchestrator and starts the streaming loop.
func (w *Worker) Start(ctx context.Context) error {
	w.logger.WithField("server", w.config.ServerAddress).Info("Starting worker")

	// Set up stream callbacks
	w.sm.SetCallbacks(
		w.onConnected,
		w.onDisconnected,
		w.onError,
	)

	// Connect to server
	if err := w.sm.Connect(w.config.ServerAddress); err != nil {
		w.logger.WithError(err).Error("Failed to connect to server")
		return err
	}

	return nil
}

// onConnected is called when the stream connects.
func (w *Worker) onConnected() error {
	w.logger.Info("Connected to orchestrator")

	// Get connection
	conn, err := w.sm.GetConnection()
	if err != nil {
		return err
	}

	// Create client
	client := proto.NewWorkerServiceClient(conn)

	// Create streaming context
	streamCtx, cancel := context.WithCancel(w.ctx)

	// Open stream
	stream, err := client.StreamTasks(streamCtx)
	if err != nil {
		cancel()
		w.logger.WithError(err).Error("Failed to create stream")
		return err
	}

	w.clientMu.Lock()
	w.client = stream
	w.clientMu.Unlock()

	w.setConnected(true)

	// Send registration message
	if err := w.sendRegistration(stream); err != nil {
		cancel()
		w.logger.WithError(err).Error("Failed to send registration")
		w.setConnected(false)
		return err
	}

	// Start processing stream
	go w.processStream(stream)

	return nil
}

// onDisconnected is called when the stream disconnects.
func (w *Worker) onDisconnected() {
	w.logger.Warn("Disconnected from orchestrator")
	w.setConnected(false)

	w.clientMu.Lock()
	w.client = nil
	w.clientMu.Unlock()
}

// onError is called when an error occurs.
func (w *Worker) onError(err error) {
	w.logger.WithError(err).Error("Stream error")
}

// sendRegistration sends the worker registration message.
func (w *Worker) sendRegistration(stream proto.WorkerService_StreamTasksClient) error {
	msg := &proto.WorkerMessage{
		WorkerId: w.config.WorkerID,
		Payload: &proto.WorkerMessage_Registration{
			Registration: &proto.WorkerRegistration{
				WorkerId:     w.config.WorkerID,
				Capabilities: w.config.Capabilities,
			},
		},
	}

	return stream.Send(msg)
}

// processStream processes incoming messages from the orchestrator.
func (w *Worker) processStream(stream proto.WorkerService_StreamTasksClient) {
	for {
		select {
		case <-w.ctx.Done():
			w.logger.Info("Stream processing stopped")
			return
		case <-w.done:
			w.logger.Info("Stream processing stopped")
			return
		default:
		}

		// Receive message
		msg, err := stream.Recv()
		if err != nil {
			w.logger.WithError(err).Error("Failed to receive message")
			w.sm.MarkDisconnected()
			return
		}

		// Process message
		w.handleOrchestratorMessage(msg, stream)
	}
}

// handleOrchestratorMessage processes a message from the orchestrator.
func (w *Worker) handleOrchestratorMessage(msg *proto.OrchestratorMessage, stream proto.WorkerService_StreamTasksClient) {
	if msg == nil {
		return
	}

	switch payload := msg.Payload.(type) {
	case *proto.OrchestratorMessage_Task:
		w.processTask(payload.Task, stream)

	case *proto.OrchestratorMessage_Ack:
		w.logger.WithField("message", payload.Ack.Message).Debug("Received acknowledgement")

	default:
		w.logger.WithField("type", fmt.Sprintf("%T", msg.Payload)).Warn("Unknown message type")
	}
}

// processTask processes a task from the orchestrator.
func (w *Worker) processTask(task *proto.Task, stream proto.WorkerService_StreamTasksClient) {
	w.logger.WithFields(logrus.Fields{
		"task_id":   task.Id,
		"task_type": task.Type,
	}).Info("Processing task")

	startTime := time.Now()

	// Get handler for task type
	w.mu.RLock()
	handler, exists := w.handlers[task.Type]
	w.mu.RUnlock()

	var result *proto.TaskResult
	if !exists {
		w.logger.WithField("task_type", task.Type).Error("No handler registered")
		result = &proto.TaskResult{
			TaskId:      task.Id,
			Success:     false,
			Error:       fmt.Sprintf("no handler for task type: %s", task.Type),
			CompletedAt: time.Now().Unix(),
		}
	} else {
		// Execute handler
		var err error
		result, err = handler(w.ctx, task)
		if err != nil {
			w.logger.WithError(err).Error("Handler execution failed")
			result = &proto.TaskResult{
				TaskId:      task.Id,
				Success:     false,
				Error:       err.Error(),
				CompletedAt: time.Now().Unix(),
			}
		}

		if result == nil {
			result = &proto.TaskResult{
				TaskId:      task.Id,
				Success:     true,
				CompletedAt: time.Now().Unix(),
			}
		}

		if result.TaskId == "" {
			result.TaskId = task.Id
		}
		if result.CompletedAt == 0 {
			result.CompletedAt = time.Now().Unix()
		}
	}

	// Record statistics
	processingTime := time.Since(startTime)
	w.recordTaskProcessed(processingTime, result.Success)

	// Send result
	resultMsg := &proto.WorkerMessage{
		WorkerId: w.config.WorkerID,
		Payload: &proto.WorkerMessage_Result{
			Result: result,
		},
	}

	if err := stream.Send(resultMsg); err != nil {
		w.logger.WithError(err).Error("Failed to send result")
		w.sm.MarkDisconnected()
		return
	}

	w.logger.WithFields(logrus.Fields{
		"task_id": task.Id,
		"success": result.Success,
		"time_ms": processingTime.Milliseconds(),
	}).Info("Task completed")
}

// recordTaskProcessed records statistics about a processed task.
func (w *Worker) recordTaskProcessed(duration time.Duration, success bool) {
	w.stats.mu.Lock()
	defer w.stats.mu.Unlock()

	w.stats.TasksProcessed++
	if !success {
		w.stats.TasksFailed++
	}

	w.stats.TotalProcessTime += duration
	if w.stats.TasksProcessed > 0 {
		w.stats.AverageTime = w.stats.TotalProcessTime / time.Duration(w.stats.TasksProcessed)
	}
	w.stats.LastProcessedTime = time.Now()
}

// IsConnected returns whether the worker is connected to the orchestrator.
func (w *Worker) IsConnected() bool {
	w.connectedMu.RLock()
	defer w.connectedMu.RUnlock()
	return w.connected
}

// setConnected sets the connection state.
func (w *Worker) setConnected(connected bool) {
	w.connectedMu.Lock()
	defer w.connectedMu.Unlock()
	w.connected = connected
}

// GetStats returns worker statistics.
func (w *Worker) GetStats() map[string]interface{} {
	w.stats.mu.RLock()
	defer w.stats.mu.RUnlock()

	return map[string]interface{}{
		"tasks_processed":    w.stats.TasksProcessed,
		"tasks_failed":       w.stats.TasksFailed,
		"total_process_time": w.stats.TotalProcessTime.String(),
		"average_time":       w.stats.AverageTime.String(),
		"last_processed":     w.stats.LastProcessedTime,
	}
}

// Stop stops the worker and closes all connections.
func (w *Worker) Stop(ctx context.Context) error {
	w.logger.Info("Stopping worker")

	close(w.done)
	w.cancel()

	// Close stream manager
	if err := w.sm.Close(); err != nil {
		w.logger.WithError(err).Warn("Error closing stream manager")
	}

	// Close client
	w.clientMu.Lock()
	if w.client != nil {
		w.client.CloseSend()
	}
	w.clientMu.Unlock()

	w.logger.Info("Worker stopped")
	return nil
}
