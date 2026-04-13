package task

import (
	"testing"
	"time"
)

func TestIsValidState(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{"Valid PENDING", StatePending, true},
		{"Valid SCHEDULED", StateScheduled, true},
		{"Valid DISPATCHED", StateDispatched, true},
		{"Valid COMPLETED", StateCompleted, true},
		{"Valid FAILED", StateFailed, true},
		{"Valid CANCELLED", StateCancelled, true},
		{"Invalid state", State("INVALID"), false},
		{"Empty state", State(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidState(tt.state)
			if got != tt.want {
				t.Errorf("IsValidState(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		to      State
		wantErr bool
	}{
		// Valid transitions
		{"PENDING -> SCHEDULED", StatePending, StateScheduled, false},
		{"PENDING -> CANCELLED", StatePending, StateCancelled, false},
		{"SCHEDULED -> DISPATCHED", StateScheduled, StateDispatched, false},
		{"SCHEDULED -> CANCELLED", StateScheduled, StateCancelled, false},
		{"DISPATCHED -> COMPLETED", StateDispatched, StateCompleted, false},
		{"DISPATCHED -> FAILED", StateDispatched, StateFailed, false},
		{"DISPATCHED -> PENDING (retry)", StateDispatched, StatePending, false},

		// Invalid transitions
		{"PENDING -> DISPATCHED", StatePending, StateDispatched, true},
		{"SCHEDULED -> COMPLETED", StateScheduled, StateCompleted, true},
		{"COMPLETED -> PENDING", StateCompleted, StatePending, true},
		{"COMPLETED -> SCHEDULED", StateCompleted, StateScheduled, true},
		{"FAILED -> PENDING", StateFailed, StatePending, true},
		{"CANCELLED -> PENDING", StateCancelled, StatePending, true},

		// Invalid states
		{"Invalid from state", State("INVALID"), StateScheduled, true},
		{"Invalid to state", StatePending, State("INVALID"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CanTransition(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("CanTransition(%s, %s) error = %v, wantErr %v", tt.from, tt.to, err, tt.wantErr)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{"COMPLETED is terminal", StateCompleted, true},
		{"FAILED is terminal", StateFailed, true},
		{"CANCELLED is terminal", StateCancelled, true},
		{"PENDING is not terminal", StatePending, false},
		{"SCHEDULED is not terminal", StateScheduled, false},
		{"DISPATCHED is not terminal", StateDispatched, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTerminal(tt.state)
			if got != tt.want {
				t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestIsTerminalFailure(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{"FAILED is terminal failure", StateFailed, true},
		{"CANCELLED is terminal failure", StateCancelled, true},
		{"COMPLETED is not terminal failure", StateCompleted, false},
		{"PENDING is not terminal failure", StatePending, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTerminalFailure(tt.state)
			if got != tt.want {
				t.Errorf("IsTerminalFailure(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestNewTask(t *testing.T) {
	payload := []byte(`{"key": "value"}`)
	task := NewTask("task-1", "wf-1", "my-task", payload)

	if task.ID != "task-1" {
		t.Errorf("ID = %v, want task-1", task.ID)
	}
	if task.WorkflowID != "wf-1" {
		t.Errorf("WorkflowID = %v, want wf-1", task.WorkflowID)
	}
	if task.Name != "my-task" {
		t.Errorf("Name = %v, want my-task", task.Name)
	}
	if task.State != StatePending {
		t.Errorf("State = %v, want %v", task.State, StatePending)
	}
	if string(task.Payload) != string(payload) {
		t.Errorf("Payload mismatch")
	}
	if task.Attempts != 0 {
		t.Errorf("Attempts = %v, want 0", task.Attempts)
	}
	if task.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %v, want 3", task.MaxAttempts)
	}
	if task.State != StatePending {
		t.Errorf("Initial state = %v, want %v", task.State, StatePending)
	}
	if task.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not set")
	}
	if task.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt not set")
	}
}

func TestTaskTransitionTo(t *testing.T) {
	task := NewTask("task-1", "wf-1", "my-task", []byte(`{}`))

	// Valid transition
	err := task.TransitionTo(StateScheduled)
	if err != nil {
		t.Fatalf("TransitionTo SCHEDULED failed: %v", err)
	}
	if task.State != StateScheduled {
		t.Errorf("State = %v, want %v", task.State, StateScheduled)
	}

	// Another valid transition
	err = task.TransitionTo(StateDispatched)
	if err != nil {
		t.Fatalf("TransitionTo DISPATCHED failed: %v", err)
	}
	if task.State != StateDispatched {
		t.Errorf("State = %v, want %v", task.State, StateDispatched)
	}
	if task.Attempts != 1 {
		t.Errorf("Attempts = %v, want 1", task.Attempts)
	}
	if task.DispatchedAt == nil {
		t.Errorf("DispatchedAt not set")
	}

	// Complete the task
	err = task.TransitionTo(StateCompleted)
	if err != nil {
		t.Fatalf("TransitionTo COMPLETED failed: %v", err)
	}
	if task.State != StateCompleted {
		t.Errorf("State = %v, want %v", task.State, StateCompleted)
	}
	if task.CompletedAt == nil {
		t.Errorf("CompletedAt not set")
	}

	// Invalid transition to PENDING from completed
	err = task.TransitionTo(StatePending)
	if err == nil {
		t.Errorf("TransitionTo PENDING from COMPLETED should fail")
	}
}

func TestTaskCanRetry(t *testing.T) {
	tests := []struct {
		name       string
		state      State
		attempts   int
		maxRetries int
		want       bool
	}{
		{"Failed task with retries available", StateFailed, 0, 3, true},
		{"Failed task at max retries", StateFailed, 3, 3, false},
		{"Cancelled task with retries", StateCancelled, 0, 3, true},
		{"Completed task with retries", StateCompleted, 0, 3, false},
		{"PENDING task", StatePending, 0, 3, false},
		{"SCHEDULED task", StateScheduled, 0, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := NewTask("task-1", "wf-1", "my-task", []byte(`{}`))
			task.State = tt.state
			task.Attempts = tt.attempts
			task.MaxAttempts = tt.maxRetries

			got := task.CanRetry()
			if got != tt.want {
				t.Errorf("CanRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskCopy(t *testing.T) {
	now := time.Now()
	original := &Task{
		ID:        "task-1",
		WorkflowID: "wf-1",
		Name:      "my-task",
		State:     StateDispatched,
		Payload:   []byte(`{"key": "value"}`),
		Result:    []byte(`{"result": "ok"}`),
		Error:     "test error",
		Attempts:  1,
		CreatedAt: now,
		UpdatedAt: now,
		DispatchedAt: &now,
		CompletedAt: &now,
	}

	copied := original.Copy()

	// Verify all fields are copied
	if copied.ID != original.ID {
		t.Errorf("ID mismatch: %s != %s", copied.ID, original.ID)
	}
	if copied.State != original.State {
		t.Errorf("State mismatch: %s != %s", copied.State, original.State)
	}

	// Verify deep copy of byte slices
	if string(copied.Payload) != string(original.Payload) {
		t.Errorf("Payload mismatch")
	}
	if string(copied.Result) != string(original.Result) {
		t.Errorf("Result mismatch")
	}

	// Verify modification doesn't affect original
	copied.Payload[0] = 'X'
	if original.Payload[0] == 'X' {
		t.Errorf("Payload not properly copied - original was modified")
	}

	// Verify pointers are deep copied
	if copied.DispatchedAt == original.DispatchedAt {
		t.Errorf("DispatchedAt not deep copied")
	}
}

func TestTaskUpdatedAtChanges(t *testing.T) {
	task := NewTask("task-1", "wf-1", "my-task", []byte(`{}`))
	original := task.UpdatedAt

	// Wait a bit and transition
	time.Sleep(10 * time.Millisecond)
	_ = task.TransitionTo(StateScheduled)

	if task.UpdatedAt == original {
		t.Errorf("UpdatedAt should change on transition")
	}
	if task.UpdatedAt.Before(original) {
		t.Errorf("UpdatedAt should be later after transition")
	}
}

func TestAllStates(t *testing.T) {
	states := AllStates()
	if len(states) != 6 {
		t.Errorf("AllStates() returned %d states, want 6", len(states))
	}

	// Verify all expected states are present
	expected := map[State]bool{
		StatePending:    true,
		StateScheduled:  true,
		StateDispatched: true,
		StateCompleted:  true,
		StateFailed:     true,
		StateCancelled:  true,
	}

	for _, state := range states {
		if !expected[state] {
			t.Errorf("Unexpected state in AllStates: %s", state)
		}
		delete(expected, state)
	}

	if len(expected) > 0 {
		t.Errorf("Missing states in AllStates: %v", expected)
	}
}
