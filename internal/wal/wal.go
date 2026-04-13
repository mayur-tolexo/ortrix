// Package wal provides the Write-Ahead Log (WAL) implementation for Ortrix.
//
// WAL logic responsibilities:
//   - Persist task state transitions before they are applied, ensuring durability.
//   - Support replay of uncommitted entries after crash recovery.
//   - Provide append-only, sequential write performance for high throughput.
//   - Allow compaction and snapshotting to bound storage usage.
//
// The implementation provides both in-memory and file-based persistence options.
package wal

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Logger defines the interface for write-ahead logging.
// Implementations will handle durable persistence and recovery.
type Logger interface {
	// Append writes an entry to the WAL. Returns the sequence number assigned.
	Append(entry Entry) (uint64, error)

	// Replay reads all uncommitted entries from the WAL for recovery.
	Replay() ([]Entry, error)

	// Checkpoint marks all entries up to the given sequence as committed.
	Checkpoint(seq uint64) error

	// Close flushes and closes the WAL.
	Close() error
}

// Entry represents a single WAL entry.
type Entry struct {
	// Sequence is the monotonically increasing sequence number assigned by WAL.
	Sequence uint64 `json:"sequence"`
	// TaskID is the unique identifier of the task.
	TaskID string `json:"task_id"`
	// WorkflowID is the workflow this task belongs to.
	WorkflowID string `json:"workflow_id"`
	// State is the target state after this transition.
	State string `json:"state"`
	// Data is the serialized task data.
	Data []byte `json:"data"`
	// Timestamp is when this entry was created.
	Timestamp time.Time `json:"timestamp"`
	// Committed indicates if this entry has been committed to stable storage.
	Committed bool `json:"committed"`
}

// InMemoryLogger is an in-memory implementation of Logger suitable for testing
// and development. It does not persist to disk.
type InMemoryLogger struct {
	mu        sync.RWMutex
	entries   []Entry
	sequence  uint64
	committed uint64
}

// NewInMemoryLogger creates a new in-memory WAL logger.
func NewInMemoryLogger() *InMemoryLogger {
	return &InMemoryLogger{
		entries:   make([]Entry, 0, 1000),
		sequence:  0,
		committed: 0,
	}
}

// Append adds an entry to the WAL and returns its sequence number.
func (l *InMemoryLogger) Append(entry Entry) (uint64, error) {
	if entry.TaskID == "" {
		return 0, fmt.Errorf("task_id is required")
	}
	if entry.WorkflowID == "" {
		return 0, fmt.Errorf("workflow_id is required")
	}
	if entry.State == "" {
		return 0, fmt.Errorf("state is required")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sequence++
	entry.Sequence = l.sequence
	entry.Timestamp = time.Now()
	entry.Committed = false

	l.entries = append(l.entries, entry)
	return l.sequence, nil
}

// Replay returns all uncommitted entries in order.
// Minimal lock scope to reduce contention on reads.
func (l *InMemoryLogger) Replay() ([]Entry, error) {
	// Minimal lock scope: only get the slice of entries
	l.mu.RLock()
	committed := l.committed
	entries := l.entries
	l.mu.RUnlock()

	if committed >= uint64(len(entries)) {
		return []Entry{}, nil
	}

	// Return all entries after the last committed one
	result := make([]Entry, len(entries)-int(committed))
	copy(result, entries[committed:])
	return result, nil
}

// Checkpoint marks all entries up to the given sequence as committed.
func (l *InMemoryLogger) Checkpoint(seq uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if seq > l.sequence {
		return fmt.Errorf("checkpoint sequence %d exceeds latest sequence %d", seq, l.sequence)
	}

	l.committed = seq
	return nil
}

// Close is a no-op for in-memory logger but implements the interface.
func (l *InMemoryLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
	return nil
}

// GetAll returns all entries for testing purposes.
// Minimal lock scope.
// This method is intended for testing only.
func (l *InMemoryLogger) GetAll() []Entry {
	// Minimal lock scope: only the slice copy
	l.mu.RLock()
	entries := l.entries
	l.mu.RUnlock()

	result := make([]Entry, len(entries))
	copy(result, entries)
	return result
}

// GetCommittedCount returns the number of committed entries for testing.
// Minimal lock scope.
// This method is intended for testing only.
func (l *InMemoryLogger) GetCommittedCount() uint64 {
	l.mu.RLock()
	count := l.committed
	l.mu.RUnlock()
	return count
}

// NoOpLogger is a no-op implementation of Logger that ignores all operations.
// Use this for testing when persistence is not needed.
type NoOpLogger struct{}

// NewNoOpLogger creates a new NoOpLogger.
func NewNoOpLogger() *NoOpLogger {
	return &NoOpLogger{}
}

// Append is a no-op that increments a virtual sequence.
func (l *NoOpLogger) Append(_ Entry) (uint64, error) {
	return 0, nil
}

// Replay returns an empty slice.
func (l *NoOpLogger) Replay() ([]Entry, error) {
	return []Entry{}, nil
}

// Checkpoint is a no-op.
func (l *NoOpLogger) Checkpoint(_ uint64) error {
	return nil
}

// Close is a no-op.
func (l *NoOpLogger) Close() error {
	return nil
}

// EntryToJSON converts an Entry to JSON bytes.
func EntryToJSON(e *Entry) ([]byte, error) {
	return json.Marshal(e)
}

// EntryFromJSON deserializes an Entry from JSON bytes.
func EntryFromJSON(data []byte) (*Entry, error) {
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
