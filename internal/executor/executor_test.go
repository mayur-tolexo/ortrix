package executor

import (
	"testing"
	"time"

	"github.com/mayur-tolexo/ortrix/internal/partition"
	"github.com/mayur-tolexo/ortrix/internal/task"
	"github.com/mayur-tolexo/ortrix/internal/wal"
	"github.com/sirupsen/logrus"
)

func newTestEngine() *Engine {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel) // Suppress logs in tests

	walLogger := wal.NewInMemoryLogger()
	partitionMgr := partition.NewDefaultManager(10)
	partitionMgr.RegisterNode("node-1")

	return NewEngine(logger, walLogger, partitionMgr)
}

func TestNewEngine(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	if e.logger == nil {
		t.Errorf("logger is nil")
	}
	if e.walLogger == nil {
		t.Errorf("walLogger is nil")
	}
	if e.partitionMgr == nil {
		t.Errorf("partitionMgr is nil")
	}
	if e.tasks == nil {
		t.Errorf("tasks map not initialized")
	}
	if e.workflowTasks == nil {
		t.Errorf("workflowTasks map not initialized")
	}
}

func TestCreateTask(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	payload := []byte(`{"key": "value"}`)
	createdTask, err := e.CreateTask("wf-1", "my-task", payload)

	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if createdTask.WorkflowID != "wf-1" {
		t.Errorf("WorkflowID = %s, want wf-1", createdTask.WorkflowID)
	}
	if createdTask.Name != "my-task" {
		t.Errorf("Name = %s, want my-task", createdTask.Name)
	}
	if createdTask.State != task.StatePending {
		t.Errorf("State = %s, want %s", createdTask.State, task.StatePending)
	}
	if string(createdTask.Payload) != string(payload) {
		t.Errorf("Payload mismatch")
	}
	if createdTask.PartitionID < 0 || createdTask.PartitionID >= 10 {
		t.Errorf("PartitionID out of range: %d", createdTask.PartitionID)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	tests := []struct {
		name       string
		workflowID string
		taskName   string
		wantErr    bool
	}{
		{"Valid", "wf-1", "task", false},
		{"Empty workflow ID", "", "task", true},
		{"Empty task name", "wf-1", "", true},
		{"Both empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.CreateTask(tt.workflowID, tt.taskName, []byte{})
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateTask error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetTask(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})
	retrieved, err := e.GetTask(created.ID)

	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("Task ID mismatch")
	}
}

func TestGetTaskNotFound(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	_, err := e.GetTask("nonexistent")
	if err == nil {
		t.Errorf("Expected error for nonexistent task")
	}
}

func TestGetTaskEmptyID(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	_, err := e.GetTask("")
	if err == nil {
		t.Errorf("Expected error for empty task ID")
	}
}

func TestTransitionTask(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})

	transitioned, err := e.TransitionTask(created.ID, task.StateScheduled)
	if err != nil {
		t.Fatalf("TransitionTask failed: %v", err)
	}

	if transitioned.State != task.StateScheduled {
		t.Errorf("State = %s, want %s", transitioned.State, task.StateScheduled)
	}

	// Verify state was persisted to WAL
	entries, _ := e.walLogger.Replay()
	if len(entries) != 2 { // One for create, one for transition
		t.Errorf("Expected 2 WAL entries, got %d", len(entries))
	}
}

func TestTransitionTaskInvalid(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})

	// Try invalid transition: PENDING -> COMPLETED (should skip SCHEDULED, DISPATCHED)
	_, err := e.TransitionTask(created.ID, task.StateCompleted)
	if err == nil {
		t.Errorf("Expected error for invalid transition")
	}
}

func TestCompleteTask(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})
	_, _ = e.TransitionTask(created.ID, task.StateScheduled)
	_, _ = e.TransitionTask(created.ID, task.StateDispatched)

	result := []byte(`{"result": "ok"}`)
	completed, err := e.CompleteTask(created.ID, result)

	if err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	if completed.State != task.StateCompleted {
		t.Errorf("State = %s, want %s", completed.State, task.StateCompleted)
	}
	if string(completed.Result) != string(result) {
		t.Errorf("Result mismatch")
	}
	if completed.CompletedAt == nil {
		t.Errorf("CompletedAt not set")
	}
}

func TestCompleteTaskWrongState(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})

	// Try to complete from PENDING (should fail)
	_, err := e.CompleteTask(created.ID, []byte{})
	if err == nil {
		t.Errorf("Expected error when completing non-dispatched task")
	}
}

func TestFailTask(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})
	_, _ = e.TransitionTask(created.ID, task.StateScheduled)
	_, _ = e.TransitionTask(created.ID, task.StateDispatched)

	failed, err := e.FailTask(created.ID, "timeout")

	if err != nil {
		t.Fatalf("FailTask failed: %v", err)
	}

	if failed.State != task.StateFailed {
		t.Errorf("State = %s, want %s", failed.State, task.StateFailed)
	}
	if failed.Error != "timeout" {
		t.Errorf("Error = %s, want timeout", failed.Error)
	}
}

func TestFailTaskWrongState(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})

	// Try to fail from PENDING (should fail)
	_, err := e.FailTask(created.ID, "error")
	if err == nil {
		t.Errorf("Expected error when failing non-dispatched task")
	}
}

func TestGetWorkflowTasks(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Create multiple tasks for same workflow
	e.CreateTask("wf-1", "task-1", []byte{})
	e.CreateTask("wf-1", "task-2", []byte{})
	e.CreateTask("wf-1", "task-3", []byte{})

	tasks, err := e.GetWorkflowTasks("wf-1")
	if err != nil {
		t.Fatalf("GetWorkflowTasks failed: %v", err)
	}

	if len(tasks) != 3 {
		t.Errorf("Expected 3 tasks, got %d", len(tasks))
	}

	// Create task for different workflow
	e.CreateTask("wf-2", "task-4", []byte{})

	tasks2, _ := e.GetWorkflowTasks("wf-2")
	if len(tasks2) != 1 {
		t.Errorf("Expected 1 task for wf-2, got %d", len(tasks2))
	}
}

func TestGetWorkflowTasksEmpty(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	tasks, err := e.GetWorkflowTasks("nonexistent")
	if err != nil {
		t.Fatalf("GetWorkflowTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Expected empty result, got %d tasks", len(tasks))
	}
}

func TestGetTasksByState(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Create tasks
	_, _ = e.CreateTask("wf-1", "task-1", []byte{})
	t2, _ := e.CreateTask("wf-1", "task-2", []byte{})

	// Keep first task in PENDING, transition t2 to SCHEDULED
	_, _ = e.TransitionTask(t2.ID, task.StateScheduled)

	pending := e.GetTasksByState(task.StatePending)
	if len(pending) != 1 {
		t.Errorf("Expected 1 PENDING task, got %d", len(pending))
	}

	scheduled := e.GetTasksByState(task.StateScheduled)
	if len(scheduled) != 1 {
		t.Errorf("Expected 1 SCHEDULED task, got %d", len(scheduled))
	}
}

func TestGetTasksForPartition(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Create multiple tasks
	createdTasks := make([]*task.Task, 5)
	for i := 0; i < 5; i++ {
		createdTasks[i], _ = e.CreateTask("wf-1", "task", []byte{})
	}

	// Find a partition that has at least one task
	partitionMap := make(map[int]int)
	for _, t := range createdTasks {
		partitionMap[t.PartitionID]++
	}

	if len(partitionMap) == 0 {
		t.Fatalf("No tasks created")
	}

	// Get tasks for first populated partition
	var targetPartition int
	for p := range partitionMap {
		targetPartition = p
		break
	}

	tasks := e.GetTasksForPartition(targetPartition)
	expectedCount := partitionMap[targetPartition]

	if len(tasks) != expectedCount {
		t.Errorf("Expected %d tasks in partition %d, got %d", expectedCount, targetPartition, len(tasks))
	}

	// Verify all tasks are in the correct partition
	for _, taskItem := range tasks {
		if taskItem.PartitionID != targetPartition {
			t.Errorf("Task partition mismatch: %d != %d", taskItem.PartitionID, targetPartition)
		}
	}
}

func TestCheckpoint(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	_, _ = e.CreateTask("wf-1", "task", []byte{})

	// Get current WAL entry count
	entries, _ := e.walLogger.Replay()
	if len(entries) == 0 {
		t.Fatalf("Expected WAL entries")
	}

	lastSeq := entries[len(entries)-1].Sequence

	// Checkpoint
	err := e.Checkpoint(lastSeq)
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// After checkpoint, replay should return empty
	replayEntries, _ := e.walLogger.Replay()
	if len(replayEntries) != 0 {
		t.Errorf("Expected empty replay after checkpoint, got %d entries", len(replayEntries))
	}
}

func TestSequenceCounter(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	if e.GetSequenceCounter() != 0 {
		t.Errorf("Initial sequence counter should be 0")
	}

	e.CreateTask("wf-1", "task", []byte{})
	seq1 := e.GetSequenceCounter()
	if seq1 != 1 {
		t.Errorf("Sequence counter after create = %d, want 1", seq1)
	}

	// Get task to retrieve it
	tasks, _ := e.GetWorkflowTasks("wf-1")
	e.TransitionTask(tasks[0].ID, task.StateScheduled)
	seq2 := e.GetSequenceCounter()

	if seq2 <= seq1 {
		t.Errorf("Sequence counter not incremented: %d -> %d", seq1, seq2)
	}
}

func TestTaskCopyIndependence(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte(`{"key": "value"}`))

	// Retrieve the task twice
	retrieved1, _ := e.GetTask(created.ID)
	_, _ = e.GetTask(created.ID)

	// Modify retrieved1 (shouldn't affect retrieved2 or internal state)
	retrieved1.Error = "modified"

	// Check internal state is unchanged
	retrieved3, _ := e.GetTask(created.ID)
	if retrieved3.Error != "" {
		t.Errorf("Internal state was modified: %s", retrieved3.Error)
	}
}

func TestCompleteTaskLifecycle(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Create -> Schedule -> Dispatch -> Complete
	created, _ := e.CreateTask("wf-1", "task", []byte(`{"input": "data"}`))

	scheduled, _ := e.TransitionTask(created.ID, task.StateScheduled)
	if scheduled.Attempts != 0 {
		t.Errorf("Attempts after schedule = %d, want 0", scheduled.Attempts)
	}

	dispatched, _ := e.TransitionTask(created.ID, task.StateDispatched)
	if dispatched.Attempts != 1 {
		t.Errorf("Attempts after dispatch = %d, want 1", dispatched.Attempts)
	}
	if dispatched.DispatchedAt == nil {
		t.Errorf("DispatchedAt not set")
	}

	completed, _ := e.CompleteTask(created.ID, []byte(`{"result": "ok"}`))
	if completed.State != task.StateCompleted {
		t.Errorf("Final state = %s, want COMPLETED", completed.State)
	}
	if completed.CompletedAt == nil {
		t.Errorf("CompletedAt not set")
	}

	// Verify WAL entries
	allEntries, _ := e.walLogger.Replay()
	if len(allEntries) < 4 {
		t.Errorf("Expected at least 4 WAL entries (create + 3 transitions), got %d", len(allEntries))
	}
}

func TestConcurrentTaskCreation(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Create tasks concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			_, err := e.CreateTask("wf-1", "task", []byte{})
			if err != nil {
				t.Errorf("CreateTask failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all tasks were created
	tasks, _ := e.GetWorkflowTasks("wf-1")
	if len(tasks) != 10 {
		t.Errorf("Expected 10 tasks, got %d", len(tasks))
	}
}

func TestTaskTimestamps(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	created, _ := e.CreateTask("wf-1", "task", []byte{})
	if created.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not set")
	}

	time.Sleep(10 * time.Millisecond)

	_, _ = e.TransitionTask(created.ID, task.StateScheduled)
	updated, _ := e.GetTask(created.ID)

	if updated.UpdatedAt.Before(created.CreatedAt) {
		t.Errorf("UpdatedAt before CreatedAt")
	}
}

func TestGetWorkflowTasksValidation(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	_, err := e.GetWorkflowTasks("")
	if err == nil {
		t.Errorf("Expected error for empty workflow ID")
	}
}
