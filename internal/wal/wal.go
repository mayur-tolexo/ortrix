// Package wal will contain the Write-Ahead Log (WAL) logic for Ortrix.
//
// WAL logic responsibilities:
//   - Persist task state transitions before they are applied, ensuring durability.
//   - Support replay of uncommitted entries after crash recovery.
//   - Provide append-only, sequential write performance for high throughput.
//   - Allow compaction and snapshotting to bound storage usage.
//
// This is a skeleton. Full implementation will follow.
package wal

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
	Sequence uint64
	TaskID   string
	State    string
	Data     []byte
}

// NoOpLogger is a placeholder WAL logger that performs no persistence.
type NoOpLogger struct{}

// NewNoOpLogger creates a new NoOpLogger.
func NewNoOpLogger() *NoOpLogger {
	return &NoOpLogger{}
}

// Append is a no-op placeholder.
func (l *NoOpLogger) Append(_ Entry) (uint64, error) {
	// TODO: Implement durable append to storage (file, embedded DB, etc.).
	return 0, nil
}

// Replay returns an empty slice (placeholder).
func (l *NoOpLogger) Replay() ([]Entry, error) {
	// TODO: Implement replay from persisted WAL entries.
	return nil, nil
}

// Checkpoint is a no-op placeholder.
func (l *NoOpLogger) Checkpoint(_ uint64) error {
	// TODO: Implement compaction of committed entries.
	return nil
}

// Close is a no-op placeholder.
func (l *NoOpLogger) Close() error {
	return nil
}
