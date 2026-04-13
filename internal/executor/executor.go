// Package executor provides the core execution engine for Ortrix.
//
// The execution engine manages the task lifecycle, state transitions,
// persistence via WAL, and coordination with partition management.
package executor

import (
	"fmt"
	"sync"
	"time"

	"github.com/mayur-tolexo/ortrix/internal/partition"
	"github.com/mayur-tolexo/ortrix/internal/task"
	"github.com/mayur-tolexo/ortrix/internal/wal"
	"github.com/sirupsen/logrus"
)

// Engine provides the core execution loop for task processing.
// It manages task state machine transitions, persistence, and partition coordination.
type Engine struct {
	mu              sync.RWMutex
	logger          *logrus.Logger
	walLogger       wal.Logger
	partitionMgr    partition.Manager
	tasks           map[string]*task.Task // taskID -> Task
	workflowTasks   map[string][]string   // workflowID -> []taskID
	sequenceCounter uint64
}

// NewEngine creates a new execution engine.
func NewEngine(logger *logrus.Logger, walLogger wal.Logger, partitionMgr partition.Manager) *Engine {
	if logger == nil {
		logger = logrus.New()
	}
	if walLogger == nil {
		walLogger = wal.NewNoOpLogger()
	}
	if partitionMgr == nil {
		partitionMgr = partition.NewDefaultManager(10)
	}

	return &Engine{
		logger:        logger,
		walLogger:     walLogger,
		partitionMgr:  partitionMgr,
		tasks:         make(map[string]*task.Task),
		workflowTasks: make(map[string][]string),
	}
}

// CreateTask creates a new task and persists it to the WAL.
// The task starts in PENDING state and is assigned to a partition.
func (e *Engine) CreateTask(workflowID, taskName string, payload []byte) (*task.Task, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("workflow_id is required")
	}
	if taskName == "" {
		return nil, fmt.Errorf("task_name is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Generate task ID
	taskID := fmt.Sprintf("task-%s-%d", workflowID, time.Now().UnixNano())

	// Create task
	newTask := task.NewTask(taskID, workflowID, taskName, payload)

	// Assign to partition
	partitionID, err := e.partitionMgr.AssignPartition(taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to assign partition: %w", err)
	}
	newTask.PartitionID = partitionID

	// Persist to WAL
	entry := wal.Entry{
		TaskID:     taskID,
		WorkflowID: workflowID,
		State:      string(task.StatePending),
		Data:       payload,
	}
	seq, err := e.walLogger.Append(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to persist task to WAL: %w", err)
	}

	e.sequenceCounter = seq

	// Store in memory
	e.tasks[taskID] = newTask
	e.workflowTasks[workflowID] = append(e.workflowTasks[workflowID], taskID)

	e.logger.WithFields(logrus.Fields{
		"task_id":     taskID,
		"workflow_id": workflowID,
		"partition":   partitionID,
		"sequence":    seq,
	}).Debug("task created")

	return newTask, nil
}

// GetTask retrieves a task by ID.
// Uses read lock only for the lookup, minimizing lock contention.
func (e *Engine) GetTask(taskID string) (*task.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	// Minimal lock scope: only for map lookup
	e.mu.RLock()
	t, exists := e.tasks[taskID]
	e.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Copy happens outside lock to reduce lock duration
	return t.Copy(), nil
}

// TransitionTask transitions a task to a new state and persists the change to WAL.
func (e *Engine) TransitionTask(taskID string, newState task.State) (*task.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := e.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Validate transition
	if err := task.CanTransition(t.State, newState); err != nil {
		return nil, fmt.Errorf("invalid state transition: %w", err)
	}

	// Persist to WAL before applying transition
	entry := wal.Entry{
		TaskID:     taskID,
		WorkflowID: t.WorkflowID,
		State:      string(newState),
		Data:       t.Payload,
	}
	seq, err := e.walLogger.Append(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to persist state transition to WAL: %w", err)
	}

	// Apply transition
	if err := t.TransitionTo(newState); err != nil {
		return nil, err
	}

	e.sequenceCounter = seq

	e.logger.WithFields(logrus.Fields{
		"task_id":     taskID,
		"old_state":   t.State,
		"new_state":   newState,
		"sequence":    seq,
	}).Debug("task transitioned")

	return t.Copy(), nil
}

// CompleteTask marks a task as completed with a result.
func (e *Engine) CompleteTask(taskID string, result []byte) (*task.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := e.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Only DISPATCHED tasks can complete
	if t.State != task.StateDispatched {
		return nil, fmt.Errorf("task must be in DISPATCHED state to complete, current: %s", t.State)
	}

	// Store result
	t.Result = result

	// Persist to WAL
	entry := wal.Entry{
		TaskID:     taskID,
		WorkflowID: t.WorkflowID,
		State:      string(task.StateCompleted),
		Data:       result,
	}
	seq, err := e.walLogger.Append(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to persist completion to WAL: %w", err)
	}

	// Apply transition
	if err := t.TransitionTo(task.StateCompleted); err != nil {
		return nil, err
	}

	e.sequenceCounter = seq

	e.logger.WithFields(logrus.Fields{
		"task_id":     taskID,
		"sequence":    seq,
	}).Debug("task completed")

	return t.Copy(), nil
}

// FailTask marks a task as failed with an error message.
func (e *Engine) FailTask(taskID, errorMsg string) (*task.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	t, exists := e.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Only DISPATCHED tasks can fail
	if t.State != task.StateDispatched {
		return nil, fmt.Errorf("task must be in DISPATCHED state to fail, current: %s", t.State)
	}

	// Store error
	t.Error = errorMsg

	// Persist to WAL
	entry := wal.Entry{
		TaskID:     taskID,
		WorkflowID: t.WorkflowID,
		State:      string(task.StateFailed),
		Data:       []byte(errorMsg),
	}
	seq, err := e.walLogger.Append(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to persist failure to WAL: %w", err)
	}

	// Apply transition
	if err := t.TransitionTo(task.StateFailed); err != nil {
		return nil, err
	}

	e.sequenceCounter = seq

	e.logger.WithFields(logrus.Fields{
		"task_id": taskID,
		"error":   errorMsg,
		"sequence": seq,
	}).Debug("task failed")

	return t.Copy(), nil
}

// GetWorkflowTasks returns all tasks for a given workflow.
// Uses minimal lock scope for better throughput.
func (e *Engine) GetWorkflowTasks(workflowID string) ([]*task.Task, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("workflow_id is required")
	}

	// Minimal lock scope: only get task IDs from map
	e.mu.RLock()
	taskIDs, exists := e.workflowTasks[workflowID]
	if !exists {
		e.mu.RUnlock()
		return []*task.Task{}, nil
	}
	// Create a copy of task IDs to avoid keeping lock during task copies
	taskIDsCopy := make([]string, len(taskIDs))
	copy(taskIDsCopy, taskIDs)
	e.mu.RUnlock()

	// Copy tasks outside lock to minimize lock duration
	tasks := make([]*task.Task, len(taskIDsCopy))
	for i, taskID := range taskIDsCopy {
		e.mu.RLock()
		t := e.tasks[taskID]
		e.mu.RUnlock()
		tasks[i] = t.Copy()
	}

	return tasks, nil
}

// GetTasksByState returns all tasks in a specific state.
// Uses minimal lock scope to reduce contention.
func (e *Engine) GetTasksByState(state task.State) []*task.Task {
	// Minimal lock scope: only iterate tasks while holding lock
	// Collect references first, then copy outside lock
	e.mu.RLock()
	tasks := make([]*task.Task, 0)
	for _, t := range e.tasks {
		if t.State == state {
			tasks = append(tasks, t)
		}
	}
	e.mu.RUnlock()

	// Copy tasks outside lock to minimize lock duration
	result := make([]*task.Task, len(tasks))
	for i, t := range tasks {
		result[i] = t.Copy()
	}
	return result
}

// GetTasksForPartition returns all tasks assigned to a specific partition.
// Uses minimal lock scope to reduce contention.
func (e *Engine) GetTasksForPartition(partitionID int) []*task.Task {
	// Minimal lock scope: collect references first
	e.mu.RLock()
	tasks := make([]*task.Task, 0)
	for _, t := range e.tasks {
		if t.PartitionID == partitionID {
			tasks = append(tasks, t)
		}
	}
	e.mu.RUnlock()

	// Copy tasks outside lock to minimize lock duration
	result := make([]*task.Task, len(tasks))
	for i, t := range tasks {
		result[i] = t.Copy()
	}
	return result
}

// Checkpoint persists a checkpoint to the WAL.
func (e *Engine) Checkpoint(seq uint64) error {
	return e.walLogger.Checkpoint(seq)
}

// GetSequenceCounter returns the current WAL sequence counter.
// Uses minimal read lock scope.
func (e *Engine) GetSequenceCounter() uint64 {
	e.mu.RLock()
	seq := e.sequenceCounter
	e.mu.RUnlock()
	return seq
}

// Close closes the WAL logger.
func (e *Engine) Close() error {
	return e.walLogger.Close()
}
