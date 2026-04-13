// Package task provides the task state machine for Ortrix.
// This package defines task states, transitions, and validation rules.
package task

import "fmt"

// State represents the current state of a task in the execution pipeline.
type State string

const (
	// StatePending indicates the task is waiting to be scheduled.
	StatePending State = "PENDING"

	// StateScheduled indicates the task has been scheduled but not yet dispatched.
	StateScheduled State = "SCHEDULED"

	// StateDispatched indicates the task has been sent to a worker.
	StateDispatched State = "DISPATCHED"

	// StateCompleted indicates the task has completed successfully.
	StateCompleted State = "COMPLETED"

	// StateFailed indicates the task has failed.
	StateFailed State = "FAILED"

	// StateCancelled indicates the task has been cancelled.
	StateCancelled State = "CANCELLED"
)

// AllStates returns all valid task states.
func AllStates() []State {
	return []State{
		StatePending,
		StateScheduled,
		StateDispatched,
		StateCompleted,
		StateFailed,
		StateCancelled,
	}
}

// IsValidState checks if a state string is a valid state.
func IsValidState(s State) bool {
	for _, valid := range AllStates() {
		if s == valid {
			return true
		}
	}
	return false
}

// CanTransition checks if a transition from one state to another is allowed.
// Returns an error if the transition is invalid.
func CanTransition(from, to State) error {
	if !IsValidState(from) {
		return fmt.Errorf("invalid from state: %s", from)
	}
	if !IsValidState(to) {
		return fmt.Errorf("invalid to state: %s", to)
	}

	// Valid transitions
	validTransitions := map[State][]State{
		StatePending:    {StateScheduled, StateCancelled},
		StateScheduled:  {StateDispatched, StateCancelled},
		StateDispatched: {StateCompleted, StateFailed, StatePending}, // Retry on failure -> PENDING
		StateCompleted:  {},                                          // Terminal state
		StateFailed:     {},                                          // Terminal state
		StateCancelled:  {},                                          // Terminal state
	}

	allowed, exists := validTransitions[from]
	if !exists {
		return fmt.Errorf("unknown from state: %s", from)
	}

	for _, valid := range allowed {
		if valid == to {
			return nil
		}
	}

	return fmt.Errorf("invalid transition from %s to %s", from, to)
}

// IsTerminal returns true if the state is terminal (no further transitions possible).
func IsTerminal(s State) bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// IsTerminalFailure returns true if the state represents a failed terminal state.
func IsTerminalFailure(s State) bool {
	return s == StateFailed || s == StateCancelled
}
