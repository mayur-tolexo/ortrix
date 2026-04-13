package wal

import (
	"testing"
	"time"
)

func TestInMemoryLoggerAppend(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	entry := Entry{
		TaskID:     "task-1",
		WorkflowID: "wf-1",
		State:      "PENDING",
		Data:       []byte(`{"key": "value"}`),
	}

	seq, err := logger.Append(entry)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if seq != 1 {
		t.Errorf("Expected sequence 1, got %d", seq)
	}

	// Append another entry
	seq2, err := logger.Append(entry)
	if err != nil {
		t.Fatalf("Second Append failed: %v", err)
	}

	if seq2 != 2 {
		t.Errorf("Expected sequence 2, got %d", seq2)
	}
}

func TestInMemoryLoggerAppendValidation(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	tests := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{
			name:    "Missing TaskID",
			entry:   Entry{WorkflowID: "wf-1", State: "PENDING"},
			wantErr: true,
		},
		{
			name:    "Missing WorkflowID",
			entry:   Entry{TaskID: "task-1", State: "PENDING"},
			wantErr: true,
		},
		{
			name:    "Missing State",
			entry:   Entry{TaskID: "task-1", WorkflowID: "wf-1"},
			wantErr: true,
		},
		{
			name:    "Valid entry",
			entry:   Entry{TaskID: "task-1", WorkflowID: "wf-1", State: "PENDING"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := logger.Append(tt.entry)
			if (err != nil) != tt.wantErr {
				t.Errorf("Append error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInMemoryLoggerReplay(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	// Add some entries
	for i := 1; i <= 5; i++ {
		entry := Entry{
			TaskID:     "task-1",
			WorkflowID: "wf-1",
			State:      "PENDING",
		}
		logger.Append(entry)
	}

	// Without checkpoint, all 5 entries should be returned
	entries, err := logger.Replay()
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(entries))
	}

	// Checkpoint at sequence 3
	err = logger.Checkpoint(3)
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// Now replay should only return 2 entries (4 and 5)
	entries, err = logger.Replay()
	if err != nil {
		t.Fatalf("Replay after checkpoint failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("Expected 2 entries after checkpoint, got %d", len(entries))
	}
	if entries[0].Sequence != 4 || entries[1].Sequence != 5 {
		t.Errorf("Replay returned wrong sequences: %d, %d", entries[0].Sequence, entries[1].Sequence)
	}
}

func TestInMemoryLoggerCheckpoint(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	// Add entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			TaskID:     "task-1",
			WorkflowID: "wf-1",
			State:      "PENDING",
		}
		logger.Append(entry)
	}

	// Valid checkpoint
	err := logger.Checkpoint(2)
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// Invalid checkpoint (exceeds sequence)
	err = logger.Checkpoint(10)
	if err == nil {
		t.Errorf("Expected error for checkpoint exceeding sequence")
	}
}

func TestInMemoryLoggerTimestamp(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	before := time.Now()
	entry := Entry{
		TaskID:     "task-1",
		WorkflowID: "wf-1",
		State:      "PENDING",
	}
	_, _ = logger.Append(entry)
	after := time.Now()

	entries := logger.GetAll()
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry")
	}

	if entries[0].Timestamp.Before(before) || entries[0].Timestamp.After(after) {
		t.Errorf("Timestamp not set correctly: %v (before %v, after %v)", entries[0].Timestamp, before, after)
	}
}

func TestInMemoryLoggerGetAll(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	// Add entries
	for i := 1; i <= 3; i++ {
		entry := Entry{
			TaskID:     "task-1",
			WorkflowID: "wf-1",
			State:      "PENDING",
		}
		logger.Append(entry)
	}

	entries := logger.GetAll()
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Verify sequences
	for i, entry := range entries {
		if entry.Sequence != uint64(i+1) {
			t.Errorf("Entry %d has sequence %d, expected %d", i, entry.Sequence, i+1)
		}
	}
}

func TestNoOpLogger(t *testing.T) {
	logger := NewNoOpLogger()

	entry := Entry{
		TaskID:     "task-1",
		WorkflowID: "wf-1",
		State:      "PENDING",
	}

	// All operations should succeed but do nothing
	seq, err := logger.Append(entry)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if seq != 0 {
		t.Errorf("NoOpLogger should return 0, got %d", seq)
	}

	entries, err := logger.Replay()
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("NoOpLogger should return empty, got %d entries", len(entries))
	}

	err = logger.Checkpoint(1)
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	err = logger.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestEntryJSON(t *testing.T) {
	entry := Entry{
		Sequence:   1,
		TaskID:     "task-1",
		WorkflowID: "wf-1",
		State:      "PENDING",
		Data:       []byte(`{"key": "value"}`),
		Timestamp:  time.Now(),
		Committed:  false,
	}

	// Serialize to JSON
	jsonBytes, err := EntryToJSON(&entry)
	if err != nil {
		t.Fatalf("EntryToJSON failed: %v", err)
	}

	// Deserialize from JSON
	deserialized, err := EntryFromJSON(jsonBytes)
	if err != nil {
		t.Fatalf("EntryFromJSON failed: %v", err)
	}

	// Verify fields match
	if deserialized.Sequence != entry.Sequence {
		t.Errorf("Sequence mismatch: %d != %d", deserialized.Sequence, entry.Sequence)
	}
	if deserialized.TaskID != entry.TaskID {
		t.Errorf("TaskID mismatch: %s != %s", deserialized.TaskID, entry.TaskID)
	}
	if deserialized.WorkflowID != entry.WorkflowID {
		t.Errorf("WorkflowID mismatch: %s != %s", deserialized.WorkflowID, entry.WorkflowID)
	}
	if deserialized.State != entry.State {
		t.Errorf("State mismatch: %s != %s", deserialized.State, entry.State)
	}
	if string(deserialized.Data) != string(entry.Data) {
		t.Errorf("Data mismatch")
	}
}

func TestEntryFromJSONInvalid(t *testing.T) {
	_, err := EntryFromJSON([]byte("invalid json"))
	if err == nil {
		t.Errorf("Expected error for invalid JSON")
	}
}

func TestInMemoryLoggerConcurrency(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	// Launch multiple goroutines appending entries
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			entry := Entry{
				TaskID:     "task-1",
				WorkflowID: "wf-1",
				State:      "PENDING",
				Data:       []byte("data"),
			}
			_, err := logger.Append(entry)
			done <- err
		}(i)
	}

	// Collect errors
	for i := 0; i < 10; i++ {
		err := <-done
		if err != nil {
			t.Fatalf("Concurrent append failed: %v", err)
		}
	}

	// Verify all entries were added
	entries := logger.GetAll()
	if len(entries) != 10 {
		t.Errorf("Expected 10 entries, got %d", len(entries))
	}
}

func TestInMemoryLoggerSequenceIncrement(t *testing.T) {
	logger := NewInMemoryLogger()
	defer logger.Close()

	var sequences []uint64
	for i := 0; i < 5; i++ {
		entry := Entry{
			TaskID:     "task-1",
			WorkflowID: "wf-1",
			State:      "PENDING",
		}
		seq, _ := logger.Append(entry)
		sequences = append(sequences, seq)
	}

	// Verify sequences are monotonically increasing
	for i := 1; i < len(sequences); i++ {
		if sequences[i] <= sequences[i-1] {
			t.Errorf("Sequence not monotonically increasing: %d <= %d", sequences[i], sequences[i-1])
		}
	}
}
