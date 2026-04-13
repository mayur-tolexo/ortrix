package partition

import (
	"testing"
)

func TestNewDefaultManager(t *testing.T) {
	tests := []struct {
		name              string
		partitionCount    int
		expectedPartitions int
	}{
		{"Positive count", 16, 16},
		{"Zero count defaults to 10", 0, 10},
		{"Negative count defaults to 10", -5, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewDefaultManager(tt.partitionCount)
			if m.GetPartitionCount() != tt.expectedPartitions {
				t.Errorf("GetPartitionCount() = %d, want %d", m.GetPartitionCount(), tt.expectedPartitions)
			}
		})
	}
}

func TestAssignPartition(t *testing.T) {
	m := NewDefaultManager(10)

	// Same task ID should always map to the same partition
	partition1, err := m.AssignPartition("task-123")
	if err != nil {
		t.Fatalf("AssignPartition failed: %v", err)
	}

	partition2, err := m.AssignPartition("task-123")
	if err != nil {
		t.Fatalf("AssignPartition failed: %v", err)
	}

	if partition1 != partition2 {
		t.Errorf("AssignPartition not consistent: %d != %d", partition1, partition2)
	}

	// Different tasks should distribute across partitions
	partitions := make(map[int]bool)
	for i := 0; i < 100; i++ {
		p, _ := m.AssignPartition("task-" + string(rune(i)))
		if p < 0 || p >= 10 {
			t.Errorf("Partition %d out of range", p)
		}
		partitions[p] = true
	}

	// Should have multiple partitions used
	if len(partitions) < 3 {
		t.Errorf("Distribution too narrow: only %d partitions used", len(partitions))
	}
}

func TestAssignPartitionValidation(t *testing.T) {
	m := NewDefaultManager(10)

	tests := []struct {
		name    string
		taskID  string
		wantErr bool
	}{
		{"Valid task ID", "task-123", false},
		{"Empty task ID", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.AssignPartition(tt.taskID)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssignPartition error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegisterNode(t *testing.T) {
	m := NewDefaultManager(10)

	err := m.RegisterNode("node-1")
	if err != nil {
		t.Fatalf("RegisterNode failed: %v", err)
	}

	nodes := m.GetNodes()
	if len(nodes) != 1 || nodes[0] != "node-1" {
		t.Errorf("Node not registered correctly")
	}
}

func TestRegisterNodeDuplicate(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	err := m.RegisterNode("node-1")

	if err == nil {
		t.Errorf("Expected error when registering duplicate node")
	}
}

func TestRegisterNodeEmptyID(t *testing.T) {
	m := NewDefaultManager(10)

	err := m.RegisterNode("")
	if err == nil {
		t.Errorf("Expected error for empty node ID")
	}
}

func TestUnregisterNode(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	m.RegisterNode("node-2")

	err := m.UnregisterNode("node-1")
	if err != nil {
		t.Fatalf("UnregisterNode failed: %v", err)
	}

	nodes := m.GetNodes()
	if len(nodes) != 1 || nodes[0] != "node-2" {
		t.Errorf("Node not unregistered correctly")
	}
}

func TestUnregisterNodeNotFound(t *testing.T) {
	m := NewDefaultManager(10)

	err := m.UnregisterNode("nonexistent")
	if err == nil {
		t.Errorf("Expected error when unregistering nonexistent node")
	}
}

func TestGetOwner(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")

	owner, err := m.GetOwner(0)
	if err != nil {
		t.Fatalf("GetOwner failed: %v", err)
	}

	if owner != "node-1" {
		t.Errorf("Owner = %s, want node-1", owner)
	}
}

func TestGetOwnerInvalidPartition(t *testing.T) {
	m := NewDefaultManager(10)
	m.RegisterNode("node-1")

	tests := []struct {
		name        string
		partitionID int
	}{
		{"Negative partition", -1},
		{"Out of range partition", 10},
		{"Far out of range", 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.GetOwner(tt.partitionID)
			if err == nil {
				t.Errorf("Expected error for partition %d", tt.partitionID)
			}
		})
	}
}

func TestGetOwnerNoNodes(t *testing.T) {
	m := NewDefaultManager(10)

	// No nodes registered
	_, err := m.GetOwner(0)
	if err == nil {
		t.Errorf("Expected error when no nodes registered")
	}
}

func TestGetPartitions(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	m.RegisterNode("node-2")

	// Get partitions for node-1 (should be 0, 2, 4, 6, 8 in round-robin)
	partitions, err := m.GetPartitions("node-1")
	if err != nil {
		t.Fatalf("GetPartitions failed: %v", err)
	}

	if len(partitions) != 5 {
		t.Errorf("Expected 5 partitions for node-1, got %d", len(partitions))
	}

	// Verify partition ownership
	for _, p := range partitions {
		owner, _ := m.GetOwner(p)
		if owner != "node-1" {
			t.Errorf("Partition %d should be owned by node-1, not %s", p, owner)
		}
	}
}

func TestGetPartitionsNotFound(t *testing.T) {
	m := NewDefaultManager(10)
	m.RegisterNode("node-1")

	_, err := m.GetPartitions("nonexistent")
	if err == nil {
		t.Errorf("Expected error for nonexistent node")
	}
}

func TestGetPartitionsEmpty(t *testing.T) {
	m := NewDefaultManager(10)

	_, err := m.GetPartitions("")
	if err == nil {
		t.Errorf("Expected error for empty node ID")
	}
}

func TestRebalance(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	m.RegisterNode("node-2")

	// Get initial partitions for node-1
	partitions1, _ := m.GetPartitions("node-1")
	count1 := len(partitions1)

	// Rebalance
	err := m.Rebalance()
	if err != nil {
		t.Fatalf("Rebalance failed: %v", err)
	}

	// After rebalance with 2 nodes, each should still have 5 partitions
	partitions1After, _ := m.GetPartitions("node-1")
	if len(partitions1After) != count1 {
		t.Errorf("Partition count changed after rebalance: %d -> %d", count1, len(partitions1After))
	}
}

func TestRebalanceOnNodeRegister(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	partitions1, _ := m.GetPartitions("node-1")

	// When we add node-2, partitions should be rebalanced
	m.RegisterNode("node-2")
	partitions1After, _ := m.GetPartitions("node-1")

	if len(partitions1After) >= len(partitions1) {
		t.Errorf("Partitions not rebalanced on node register")
	}
}

func TestRebalanceOnNodeUnregister(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	m.RegisterNode("node-2")

	partitions1, _ := m.GetPartitions("node-1")
	partitions2, _ := m.GetPartitions("node-2")

	if len(partitions1) != 5 || len(partitions2) != 5 {
		t.Errorf("Unexpected initial partition count")
	}

	// Unregister node-2
	m.UnregisterNode("node-2")
	partitions1After, _ := m.GetPartitions("node-1")

	if len(partitions1After) != 10 {
		t.Errorf("Expected 10 partitions for node-1 after unregister, got %d", len(partitions1After))
	}
}

func TestGetNodes(t *testing.T) {
	m := NewDefaultManager(10)

	m.RegisterNode("node-1")
	m.RegisterNode("node-2")
	m.RegisterNode("node-3")

	nodes := m.GetNodes()
	if len(nodes) != 3 {
		t.Errorf("Expected 3 nodes, got %d", len(nodes))
	}

	// Verify all nodes are present
	nodeMap := make(map[string]bool)
	for _, n := range nodes {
		nodeMap[n] = true
	}

	for _, expected := range []string{"node-1", "node-2", "node-3"} {
		if !nodeMap[expected] {
			t.Errorf("Node %s not found in GetNodes", expected)
		}
	}
}

func TestGetNodesEmpty(t *testing.T) {
	m := NewDefaultManager(10)

	nodes := m.GetNodes()
	if len(nodes) != 0 {
		t.Errorf("Expected 0 nodes, got %d", len(nodes))
	}
}

func TestMultiplePartitionCounts(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{"5 partitions", 5},
		{"10 partitions", 10},
		{"20 partitions", 20},
		{"100 partitions", 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewDefaultManager(tt.count)

			if m.GetPartitionCount() != tt.count {
				t.Errorf("GetPartitionCount() = %d, want %d", m.GetPartitionCount(), tt.count)
			}

			// Register nodes and verify distribution
			m.RegisterNode("node-1")
			m.RegisterNode("node-2")

			p1, _ := m.GetPartitions("node-1")
			p2, _ := m.GetPartitions("node-2")

			if len(p1)+len(p2) != tt.count {
				t.Errorf("Total partitions = %d, want %d", len(p1)+len(p2), tt.count)
			}
		})
	}
}

func TestConsistentHashing(t *testing.T) {
	m := NewDefaultManager(10)

	// Test that same task always goes to same partition
	taskID := "consistent-task"
	expectedPartition, _ := m.AssignPartition(taskID)

	for i := 0; i < 100; i++ {
		p, _ := m.AssignPartition(taskID)
		if p != expectedPartition {
			t.Errorf("Consistent hashing failed: partition changed from %d to %d", expectedPartition, p)
		}
	}
}
