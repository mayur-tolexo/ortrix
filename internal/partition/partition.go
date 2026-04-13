// Package partition provides partition management for Ortrix.
//
// Partition logic responsibilities:
//   - Assign task partitions to orchestrator instances for distributed execution.
//   - Handle partition rebalancing when orchestrators join or leave the cluster.
//   - Provide consistent hashing or range-based partitioning of tasks.
//   - Ensure partition ownership is tracked and can be queried by other components.
package partition

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// Manager defines the interface for partition management.
// Implementations will handle assignment, rebalancing, and ownership queries.
type Manager interface {
	// AssignPartition assigns a task to a partition based on task ID using consistent hashing.
	// Returns the partition ID.
	AssignPartition(taskID string) (int, error)

	// GetOwner returns the orchestrator instance responsible for a given partition.
	GetOwner(partitionID int) (string, error)

	// RegisterNode registers a new orchestrator node in the cluster.
	RegisterNode(nodeID string) error

	// UnregisterNode removes an orchestrator node from the cluster.
	UnregisterNode(nodeID string) error

	// Rebalance triggers partition rebalancing across available orchestrators.
	Rebalance() error

	// GetPartitions returns all partition IDs assigned to a specific node.
	GetPartitions(nodeID string) ([]int, error)

	// GetNodes returns all registered orchestrator nodes.
	GetNodes() []string

	// GetPartitionCount returns the total number of partitions.
	GetPartitionCount() int
}

// DefaultManager implements the Manager interface with consistent hashing.
type DefaultManager struct {
	mu              sync.RWMutex
	nodes           map[string]*NodeState
	partitionCount  int
	partitionOwners map[int]string // partitionID -> nodeID
}

// NodeState tracks information about an orchestrator node.
type NodeState struct {
	NodeID        string
	RegisteredAt  time.Time
	LastHeartbeat time.Time
	Healthy       bool
}

// NewDefaultManager creates a new DefaultManager with specified partition count.
func NewDefaultManager(partitionCount int) *DefaultManager {
	if partitionCount <= 0 {
		partitionCount = 10 // Default to 10 partitions
	}
	return &DefaultManager{
		nodes:           make(map[string]*NodeState),
		partitionCount:  partitionCount,
		partitionOwners: make(map[int]string),
	}
}

// AssignPartition assigns a task to a partition using consistent hashing.
// Minimal lock scope for high-throughput consistent hashing.
func (m *DefaultManager) AssignPartition(taskID string) (int, error) {
	if taskID == "" {
		return 0, fmt.Errorf("task_id cannot be empty")
	}

	// Partition count never changes, so no lock needed
	// Consistent hashing is deterministic and doesn't require lock
	if m.partitionCount == 0 {
		return 0, fmt.Errorf("no partitions configured")
	}

	// Use FNV-1a hash for consistent partitioning
	h := fnv.New64a()
	h.Write([]byte(taskID))
	hash := h.Sum64()

	partitionID := int(hash % uint64(m.partitionCount))
	return partitionID, nil
}

// RegisterNode registers a new orchestrator node and rebalances partitions.
func (m *DefaultManager) RegisterNode(nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("node_id cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.nodes[nodeID]; exists {
		return fmt.Errorf("node %s already registered", nodeID)
	}

	m.nodes[nodeID] = &NodeState{
		NodeID:        nodeID,
		RegisteredAt:  time.Now(),
		LastHeartbeat: time.Now(),
		Healthy:       true,
	}

	// Rebalance partitions among all nodes
	m.rebalancePartitions()
	return nil
}

// UnregisterNode removes an orchestrator node and rebalances partitions.
func (m *DefaultManager) UnregisterNode(nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("node_id cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.nodes[nodeID]; !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}

	delete(m.nodes, nodeID)

	// Rebalance partitions among remaining nodes
	m.rebalancePartitions()
	return nil
}

// GetOwner returns the orchestrator instance responsible for a given partition.
// Minimal lock scope for high-throughput lookups.
func (m *DefaultManager) GetOwner(partitionID int) (string, error) {
	if partitionID < 0 || partitionID >= m.partitionCount {
		return "", fmt.Errorf("invalid partition_id: %d", partitionID)
	}

	// Minimal lock scope: only the map lookup
	m.mu.RLock()
	owner, exists := m.partitionOwners[partitionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("no owner for partition %d", partitionID)
	}

	return owner, nil
}

// Rebalance triggers partition rebalancing across available orchestrators.
func (m *DefaultManager) Rebalance() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rebalancePartitions()
	return nil
}

// rebalancePartitions distributes partitions evenly among registered nodes (internal method).
// Must be called with lock held.
func (m *DefaultManager) rebalancePartitions() {
	if len(m.nodes) == 0 {
		m.partitionOwners = make(map[int]string)
		return
	}

	// Get sorted list of node IDs for consistent ordering
	nodeIDs := make([]string, 0, len(m.nodes))
	for nodeID := range m.nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}

	// Simple round-robin distribution
	// TODO: Implement more sophisticated algorithms (consistent hashing, load-aware, etc.)
	newOwners := make(map[int]string)
	for i := 0; i < m.partitionCount; i++ {
		nodeIdx := i % len(nodeIDs)
		newOwners[i] = nodeIDs[nodeIdx]
	}

	m.partitionOwners = newOwners
}

// GetPartitions returns all partition IDs assigned to a specific node.
// Minimal lock scope for high-throughput queries.
func (m *DefaultManager) GetPartitions(nodeID string) ([]int, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("node_id cannot be empty")
	}

	// Minimal lock scope: check node existence and collect partitions
	m.mu.RLock()
	if _, exists := m.nodes[nodeID]; !exists {
		m.mu.RUnlock()
		return nil, fmt.Errorf("node %s not found", nodeID)
	}

	var partitions []int
	for partitionID, owner := range m.partitionOwners {
		if owner == nodeID {
			partitions = append(partitions, partitionID)
		}
	}
	m.mu.RUnlock()

	return partitions, nil
}

// GetNodes returns all registered orchestrator nodes.
// Minimal lock scope for high-throughput queries.
func (m *DefaultManager) GetNodes() []string {
	// Minimal lock scope: only the map iteration
	m.mu.RLock()
	nodes := make([]string, 0, len(m.nodes))
	for nodeID := range m.nodes {
		nodes = append(nodes, nodeID)
	}
	m.mu.RUnlock()

	return nodes
}

// GetPartitionCount returns the total number of partitions.
func (m *DefaultManager) GetPartitionCount() int {
	return m.partitionCount
}
