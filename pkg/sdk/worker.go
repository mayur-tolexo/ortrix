// Package sdk provides the worker SDK for Flowd.
// Workers use this SDK to register capabilities, connect to the orchestrator,
// and receive tasks for execution.
package sdk

import (
	"context"
	"fmt"
	"sync"
)

// TaskHandler is a function that processes a task payload and returns a result or error.
type TaskHandler func(ctx context.Context, taskID string, payload []byte) ([]byte, error)

// Worker represents a Flowd worker that can handle tasks.
type Worker struct {
	mu           sync.RWMutex
	id           string
	handlers     map[string]TaskHandler
	capabilities []string
}

// NewWorker creates a new worker with the given ID.
func NewWorker(id string) *Worker {
	return &Worker{
		id:           id,
		handlers:     make(map[string]TaskHandler),
		capabilities: make([]string, 0),
	}
}

// RegisterHandler registers a handler for a given task type.
// This also adds the task type to the worker's capabilities.
func (w *Worker) RegisterHandler(taskType string, handler TaskHandler) error {
	if taskType == "" {
		return fmt.Errorf("task type must not be empty")
	}
	if handler == nil {
		return fmt.Errorf("handler must not be nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.handlers[taskType] = handler
	w.capabilities = append(w.capabilities, taskType)
	return nil
}

// GetHandler returns the handler for a given task type.
func (w *Worker) GetHandler(taskType string) (TaskHandler, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	h, ok := w.handlers[taskType]
	return h, ok
}

// Capabilities returns the list of task types this worker can handle.
func (w *Worker) Capabilities() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	caps := make([]string, len(w.capabilities))
	copy(caps, w.capabilities)
	return caps
}

// ID returns the worker's unique identifier.
func (w *Worker) ID() string {
	return w.id
}

// Start connects to the orchestrator and begins receiving tasks.
// This is a skeleton — the full gRPC streaming implementation will be added later.
func (w *Worker) Start(_ context.Context, _ string) error {
	// TODO: Establish bidirectional gRPC stream with the orchestrator.
	// TODO: Send WorkerRegistration with capabilities.
	// TODO: Enter receive loop to process dispatched tasks.
	// TODO: Send TaskResult back over the stream.
	return fmt.Errorf("worker start not yet implemented")
}
