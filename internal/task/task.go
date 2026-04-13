package task

import (
	"time"
)

// Task represents a unit of work in the Ortrix system.
type Task struct {
	// ID is the unique identifier for this task.
	ID string

	// WorkflowID is the ID of the workflow this task belongs to.
	WorkflowID string

	// Name is the human-readable name of the task.
	Name string

	// State is the current state of the task in the execution pipeline.
	State State

	// Payload is the JSON-encoded input data for this task.
	Payload []byte

	// Result is the JSON-encoded output data produced by this task (if completed).
	Result []byte

	// Error is the error message if the task failed.
	Error string

	// PartitionID is the partition this task is assigned to.
	PartitionID int

	// Attempts is the number of times this task has been attempted.
	Attempts int

	// MaxAttempts is the maximum number of times this task should be retried.
	MaxAttempts int

	// CreatedAt is the time the task was created.
	CreatedAt time.Time

	// UpdatedAt is the time the task state was last updated.
	UpdatedAt time.Time

	// DispatchedAt is the time the task was dispatched to a worker (if dispatched).
	DispatchedAt *time.Time

	// CompletedAt is the time the task completed (if completed).
	CompletedAt *time.Time
}

// NewTask creates a new task with initial state PENDING.
func NewTask(id, workflowID, name string, payload []byte) *Task {
	now := time.Now()
	return &Task{
		ID:          id,
		WorkflowID:  workflowID,
		Name:        name,
		State:       StatePending,
		Payload:     payload,
		PartitionID: 0, // Will be assigned by partition manager
		Attempts:    0,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// TransitionTo transitions the task to a new state if the transition is valid.
// Returns an error if the transition is invalid.
func (t *Task) TransitionTo(newState State) error {
	if err := CanTransition(t.State, newState); err != nil {
		return err
	}

	t.State = newState
	t.UpdatedAt = time.Now()

	// Update transition-specific fields
	switch newState {
	case StateDispatched:
		now := time.Now()
		t.DispatchedAt = &now
		t.Attempts++
	case StateCompleted:
		now := time.Now()
		t.CompletedAt = &now
	}

	return nil
}

// CanRetry returns true if the task has not exceeded max attempts and is in a retryable state.
func (t *Task) CanRetry() bool {
	return t.Attempts < t.MaxAttempts && IsTerminalFailure(t.State)
}

// Copy creates a shallow copy of the task.
func (t *Task) Copy() *Task {
	result := *t
	if t.Result != nil {
		result.Result = make([]byte, len(t.Result))
		copy(result.Result, t.Result)
	}
	if t.Payload != nil {
		result.Payload = make([]byte, len(t.Payload))
		copy(result.Payload, t.Payload)
	}
	if t.DispatchedAt != nil {
		dispatchedAt := *t.DispatchedAt
		result.DispatchedAt = &dispatchedAt
	}
	if t.CompletedAt != nil {
		completedAt := *t.CompletedAt
		result.CompletedAt = &completedAt
	}
	return &result
}
